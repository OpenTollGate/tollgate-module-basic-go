// Declarative multi-step captive-portal recipes.
//
// The single-form engine in portal_auto.go covers the common "one page, one
// submit" portal. Real airports and hotels often need more: a redirect, an
// email registration, a plan selection and a controller callback. This file
// implements a small, Go-native subset of that recipe model:
//
//	match -> flow (request | attempts | skip_if | fail_if | form_generic | wispr)
//	      -> verify
//
// It is deliberately not a general HTTP automation language. Every request is
// bound to the target uplink (PortalBinding), every destination must be the
// first host or an explicit allow-list entry, credentials come from outside the
// recipe, paid recipes never auto-run, and the `identity` strategy is rejected
// outright: rotating a MAC or node key to obtain another entitlement is not a
// supported behaviour.
package wireless_gateway_manager

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PortalRecipeSpec is one declarative login procedure.
type PortalRecipeSpec struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	SchemaVersion int               `json:"schema_version,omitempty"`
	Description   string            `json:"description,omitempty"`
	Paid          bool              `json:"paid,omitempty"`
	Credentials   bool              `json:"credentials,omitempty"`
	Match         json.RawMessage   `json:"match,omitempty"`
	Flow          PortalRecipeFlow  `json:"flow"`
	Identity      json.RawMessage   `json:"identity,omitempty"`
	AllowHosts    []string          `json:"allow_hosts,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	// SeedVars are runtime variables supplied by the caller (for example the
	// discovered start_url). They are never serialized and never contain
	// credentials.
	SeedVars map[string]string `json:"-"`
}

// PortalRecipeFlow is the ordered procedure. Only "http-api" is executed;
// "recorded" is rejected until a replay format is defined.
type PortalRecipeFlow struct {
	Type  string             `json:"type"`
	Steps []PortalRecipeStep `json:"steps,omitempty"`
	Path  string             `json:"path,omitempty"`
}

// PortalRecipeStep is one step. A step carries the fields for its own type;
// unused fields are ignored.
type PortalRecipeStep struct {
	Type string `json:"type"`

	// request
	Method     string            `json:"method,omitempty"`
	URL        string            `json:"url,omitempty"`
	Body       json.RawMessage   `json:"body,omitempty"`
	Form       map[string]string `json:"form,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	AuthFrom   string            `json:"auth_from,omitempty"`
	AuthHeader string            `json:"auth_header,omitempty"`
	Vars       map[string]string `json:"vars,omitempty"`
	// HeaderVars extracts response headers (for example Location) into the
	// context. It is our extension; the original recipe model only extracted
	// JSON bodies, which is not enough for an Aruba controller redirect.
	HeaderVars map[string]string `json:"header_vars,omitempty"`
	// HTMLVars extracts a regex capture group from the response body into the
	// context. It is how a CSRF token or a meta-refresh target is read without
	// a full HTML parser.
	HTMLVars map[string]string `json:"html_vars,omitempty"`
	Retries  int               `json:"retries,omitempty"`
	// NoRedirect keeps a 3xx visible to HeaderVars instead of following it.
	NoRedirect bool `json:"no_redirect,omitempty"`

	// attempts
	Candidates [][]PortalRecipeStep `json:"candidates,omitempty"`

	// skip_if / fail_if
	Condition string  `json:"condition,omitempty"`
	Var       string  `json:"var,omitempty"`
	Min       float64 `json:"min,omitempty"`
	Reason    string  `json:"reason,omitempty"`
}

// PortalRecipeCredentials are supplied by the caller, never by the recipe.
type PortalRecipeCredentials struct {
	Username string
	Password string
}

// PortalRecipeResult is the outcome of one recipe run.
type PortalRecipeResult struct {
	OK     bool
	Reason string
	Steps  int
}

const (
	recipeTimeout = 10 * time.Second
	recipeMaxBody = 128 << 10
)

// LoadPortalRecipe parses and validates a recipe. It rejects an `identity`
// strategy: rotating a MAC or node key is not an access mechanism.
func LoadPortalRecipe(data []byte) (PortalRecipeSpec, error) {
	var spec PortalRecipeSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return spec, fmt.Errorf("portal recipe: %w", err)
	}
	if err := validatePortalRecipe(spec); err != nil {
		return spec, err
	}
	return spec, nil
}

func validatePortalRecipe(spec PortalRecipeSpec) error {
	if strings.TrimSpace(spec.ID) == "" {
		return fmt.Errorf("portal recipe: id is required")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("portal recipe: name is required")
	}
	if len(spec.Identity) > 0 && string(spec.Identity) != "null" {
		return fmt.Errorf("portal recipe %q: identity strategies are not supported", spec.ID)
	}
	if spec.Flow.Type != "http-api" {
		return fmt.Errorf("portal recipe %q: flow.type must be http-api", spec.ID)
	}
	if len(spec.Flow.Steps) == 0 {
		return fmt.Errorf("portal recipe %q: flow.steps is empty", spec.ID)
	}
	for i, step := range spec.Flow.Steps {
		switch step.Type {
		case "request":
			if strings.TrimSpace(step.URL) == "" {
				return fmt.Errorf("portal recipe %q: step %d request has no url", spec.ID, i)
			}
		case "attempts":
			if len(step.Candidates) == 0 {
				return fmt.Errorf("portal recipe %q: step %d attempts has no candidates", spec.ID, i)
			}
		case "skip_if", "fail_if":
			if strings.TrimSpace(step.Var) == "" {
				return fmt.Errorf("portal recipe %q: step %d %s has no var", spec.ID, i, step.Type)
			}
		case "form_generic", "wispr":
			if strings.TrimSpace(step.URL) == "" {
				return fmt.Errorf("portal recipe %q: step %d %s has no url", spec.ID, i, step.Type)
			}
		default:
			return fmt.Errorf("portal recipe %q: step %d has unsupported type %q", spec.ID, i, step.Type)
		}
	}
	return nil
}

// ExecutePortalRecipe runs a recipe through binding. probe, when non-nil, must
// check connectivity through the same uplink; a recipe is not successful just
// because its last HTTP request returned.
func ExecutePortalRecipe(ctx context.Context, binding PortalBinding, spec PortalRecipeSpec, creds PortalRecipeCredentials, probe func(context.Context) bool) PortalRecipeResult {
	if len(spec.Identity) > 0 && string(spec.Identity) != "null" {
		return PortalRecipeResult{Reason: "identity-not-allowed"}
	}
	if spec.Paid {
		return PortalRecipeResult{Reason: "paid-recipe-requires-human"}
	}
	if spec.Credentials && (creds.Username == "" || creds.Password == "") {
		return PortalRecipeResult{Reason: "credentials-missing"}
	}
	if spec.Flow.Type != "http-api" || len(spec.Flow.Steps) == 0 {
		return PortalRecipeResult{Reason: "unsupported-flow"}
	}

	jar, _ := cookiejar.New(nil)
	netRecipe := portalRecipe{
		timeout:    recipeTimeout,
		mark:       binding.Mark,
		dns:        binding.DNS,
		allowHosts: append([]string(nil), spec.AllowHosts...),
	}
	vars := map[string]string{"user": creds.Username, "pass": creds.Password}
	for name, value := range spec.SeedVars {
		vars[name] = value
	}
	run := &portalRecipeRun{
		ctx:     ctx,
		spec:    spec,
		creds:   creds,
		binding: portalBinding{iface: binding.Interface, sourceIP: binding.SourceIP},
		recipe:  netRecipe,
		jar:     jar,
		client:  portalClient(netRecipe, portalBinding{iface: binding.Interface, sourceIP: binding.SourceIP}, true, jar),
		vars:    vars,
		probeFn: probe,
	}

	steps := 0
	for _, step := range spec.Flow.Steps {
		steps++
		if err := run.runStep(step); err != nil {
			if err == errRecipeSatisfied {
				return PortalRecipeResult{OK: true, Reason: "satisfied", Steps: steps}
			}
			return PortalRecipeResult{Reason: err.Error(), Steps: steps}
		}
	}
	if probe != nil && !probe(ctx) {
		return PortalRecipeResult{Reason: "still-captive", Steps: steps}
	}
	return PortalRecipeResult{OK: true, Reason: "ok", Steps: steps}
}

var errRecipeSatisfied = fmt.Errorf("recipe-satisfied")

type portalRecipeRun struct {
	ctx     context.Context
	spec    PortalRecipeSpec
	creds   PortalRecipeCredentials
	binding portalBinding
	recipe  portalRecipe
	jar     http.CookieJar
	client  *http.Client
	vars    map[string]string
	primary string
	probeFn func(context.Context) bool
}

func (r *portalRecipeRun) runStep(step PortalRecipeStep) error {
	switch step.Type {
	case "request":
		return r.doRequest(step)
	case "attempts":
		return r.doAttempts(step)
	case "skip_if":
		value, err := strconv.ParseFloat(strings.TrimSpace(r.vars[step.Var]), 64)
		if err == nil && value >= step.Min {
			return errRecipeSatisfied
		}
		return nil
	case "fail_if":
		if step.Condition == "var_empty" && strings.TrimSpace(r.vars[step.Var]) == "" {
			reason := step.Reason
			if reason == "" {
				reason = "required variable is empty"
			}
			return fmt.Errorf("%s", reason)
		}
		return nil
	case "form_generic":
		return r.doFormGeneric(step)
	case "wispr":
		return r.doWispr(step)
	default:
		return fmt.Errorf("unsupported step type %q", step.Type)
	}
}

func (r *portalRecipeRun) doAttempts(step PortalRecipeStep) error {
	for _, group := range step.Candidates {
		failed := false
		for _, request := range group {
			if request.Type == "" {
				request.Type = "request"
			}
			if err := r.doRequest(request); err != nil {
				failed = true
				break
			}
		}
		if failed {
			continue
		}
		if r.probe() {
			return nil
		}
	}
	return fmt.Errorf("none of the candidate attempts brought the uplink online")
}

func (r *portalRecipeRun) probe() bool {
	// The probe is injected into the run so attempts can stop at the first
	// candidate that works. Executor callers that do not supply one rely on the
	// final probe only.
	if r.probeFn == nil {
		return false
	}
	return r.probeFn(r.ctx)
}

func (r *portalRecipeRun) doRequest(step PortalRecipeStep) error {
	target, err := r.allowedURL(step.URL)
	if err != nil {
		return err
	}
	method := strings.ToUpper(strings.TrimSpace(step.Method))
	if method == "" {
		method = http.MethodGet
	}

	buildBody := func() (io.Reader, string) {
		switch {
		case len(step.Form) > 0:
			values := url.Values{}
			for k, v := range step.Form {
				values.Set(k, r.render(v))
			}
			return strings.NewReader(values.Encode()), "application/x-www-form-urlencoded"
		case len(step.Body) > 0:
			rendered := r.render(string(step.Body))
			contentType := ""
			trimmed := strings.TrimSpace(rendered)
			if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
				contentType = "application/json"
			}
			return strings.NewReader(rendered), contentType
		default:
			return nil, ""
		}
	}

	buildRequest := func() (*http.Request, error) {
		body, contentType := buildBody()
		req, err := http.NewRequestWithContext(r.ctx, method, target, body)
		if err != nil {
			return nil, err
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		for k, v := range step.Headers {
			req.Header.Set(k, r.render(v))
		}
		if step.AuthFrom != "" {
			header := step.AuthHeader
			if header == "" {
				header = "auth-token"
			}
			if token := r.vars[step.AuthFrom]; token != "" {
				req.Header.Set(header, token)
			}
		}
		return req, nil
	}

	client := r.client
	if step.NoRedirect {
		clone := *r.client
		clone.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		client = &clone
	}

	var resp *http.Response
	for attempt := 0; attempt <= step.Retries; attempt++ {
		req, reqErr := buildRequest()
		if reqErr != nil {
			return reqErr
		}
		resp, reqErr = client.Do(req)
		if reqErr == nil {
			err = nil
			break
		}
		err = reqErr
	}
	if err != nil || resp == nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, recipeMaxBody))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s returned HTTP %d", method, target, resp.StatusCode)
	}
	if resp.Request != nil && resp.Request.URL != nil {
		r.vars["current_url"] = resp.Request.URL.String()
		r.vars["origin"] = resp.Request.URL.Scheme + "://" + resp.Request.URL.Host
	}
	for name, pattern := range step.HTMLVars {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("html_vars %s: %w", name, err)
		}
		match := re.FindStringSubmatch(string(raw))
		if len(match) < 2 {
			return fmt.Errorf("html_vars %s: no match", name)
		}
		r.vars[name] = html.UnescapeString(match[1])
	}
	for name, path := range step.Vars {
		value, err := jsonPathLite(raw, path)
		if err != nil {
			return fmt.Errorf("extract %s: %w", name, err)
		}
		r.vars[name] = value
	}
	for name, header := range step.HeaderVars {
		r.vars[name] = resp.Header.Get(header)
	}
	return nil
}

func (r *portalRecipeRun) allowedURL(raw string) (string, error) {
	raw = strings.TrimSpace(r.render(raw))
	if strings.HasPrefix(raw, "/") && r.vars["origin"] != "" {
		raw = r.vars["origin"] + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("recipe URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("recipe URL must be http(s) with a host")
	}
	if r.primary == "" {
		r.primary = strings.ToLower(u.Host)
	}
	if len(r.spec.AllowHosts) > 0 {
		if !portalHostAllowed(u, r.spec.AllowHosts) && strings.ToLower(u.Host) != r.primary {
			return "", fmt.Errorf("recipe URL host %s is not allow-listed", u.Host)
		}
		return u.String(), nil
	}
	if strings.ToLower(u.Host) != r.primary {
		return "", fmt.Errorf("recipe URL host %s differs from the first host %s", u.Host, r.primary)
	}
	return u.String(), nil
}

func (r *portalRecipeRun) doFormGeneric(step PortalRecipeStep) error {
	target, err := r.allowedURL(step.URL)
	if err != nil {
		return err
	}
	page, base, err := fetchPortalPage(r.ctx, target, r.recipe, r.binding, r.jar)
	if err != nil {
		return err
	}
	if looksLikePaymentForm(page) {
		return fmt.Errorf("refusing to auto-fill a page that looks like a payment form")
	}
	form, err := parsePortalForm(page, base, r.spec.AllowHosts)
	if err != nil {
		return err
	}
	fillGenericForm(form, page, r.creds)
	return submitPortalForm(r.ctx, form, r.recipe, r.binding, r.jar)
}

func (r *portalRecipeRun) doWispr(step PortalRecipeStep) error {
	target, err := r.allowedURL(step.URL)
	if err != nil {
		return err
	}
	page, base, err := fetchPortalPage(r.ctx, target, r.recipe, r.binding, r.jar)
	if err != nil {
		return err
	}
	loginURL := wisprLoginURL(page)
	if loginURL == "" {
		return fmt.Errorf("no WISPr LoginURL on the portal page")
	}
	ref, err := url.Parse(html.UnescapeString(loginURL))
	if err != nil {
		return err
	}
	action := base.ResolveReference(ref)
	if !portalHostAllowed(action, r.spec.AllowHosts) && strings.ToLower(action.Host) != r.primary {
		return fmt.Errorf("WISPr LoginURL host %s is not allow-listed", action.Host)
	}
	values := url.Values{}
	values.Set("LoginUsername", r.creds.Username)
	values.Set("LoginPassword", r.creds.Password)
	values.Set("username", r.creds.Username)
	values.Set("password", r.creds.Password)
	req, err := http.NewRequestWithContext(r.ctx, http.MethodPost, action.String(), strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, recipeMaxBody))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("WISPr login returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (r *portalRecipeRun) render(s string) string {
	for name, value := range r.vars {
		s = strings.ReplaceAll(s, "{{"+name+"}}", value)
	}
	return s
}

func wisprLoginURL(page string) string {
	lower := strings.ToLower(page)
	start := strings.Index(lower, "<loginurl>")
	if start < 0 {
		return ""
	}
	end := strings.Index(lower[start:], "</loginurl>")
	if end < 0 {
		return ""
	}
	value := page[start+len("<loginurl>") : start+end]
	return strings.TrimSpace(html.UnescapeString(value))
}

func fillGenericForm(form *portalForm, page string, creds PortalRecipeCredentials) {
	for _, input := range portalInputRe.FindAllStringSubmatch(page, -1) {
		attrs := parsePortalAttrs(input[1])
		name := strings.TrimSpace(attrs["name"])
		if name == "" {
			continue
		}
		typ := strings.ToLower(strings.TrimSpace(attrs["type"]))
		lowerName := strings.ToLower(name)
		switch {
		case typ == "password":
			form.fields.Set(name, creds.Password)
		case typ == "email" || strings.Contains(lowerName, "user") || strings.Contains(lowerName, "email") || strings.Contains(lowerName, "login"):
			if form.fields.Get(name) == "" {
				form.fields.Set(name, creds.Username)
			}
		}
	}
}

func looksLikePaymentForm(page string) bool {
	lower := strings.ToLower(page)
	hasPassword := strings.Contains(lower, `type="password"`) || strings.Contains(lower, `type='password'`)
	if !hasPassword {
		return false
	}
	for _, marker := range []string{"cvc", "cvv", "card number", "cardnumber", "expiry", "expiration", "billing"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// jsonPathLite supports $.a.b and $.a.0.b. The filter form is intentionally
// unsupported: it is not needed for the recipes we ship, and a partial
// implementation would silently extract the wrong value.
func jsonPathLite(body []byte, path string) (string, error) {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "$.") {
		return "", fmt.Errorf("unsupported path %q", path)
	}
	var current interface{}
	if err := json.Unmarshal(body, &current); err != nil {
		return "", err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "$."), ".") {
		if part == "" {
			continue
		}
		switch node := current.(type) {
		case map[string]interface{}:
			value, ok := node[part]
			if !ok {
				return "", fmt.Errorf("path %q: missing key %q", path, part)
			}
			current = value
		case []interface{}:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(node) {
				return "", fmt.Errorf("path %q: bad index %q", path, part)
			}
			current = node[index]
		default:
			return "", fmt.Errorf("path %q: cannot descend into %T", path, current)
		}
	}
	switch value := current.(type) {
	case string:
		return value, nil
	case float64:
		if value == float64(int64(value)) {
			return strconv.FormatInt(int64(value), 10), nil
		}
		return strconv.FormatFloat(value, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(value), nil
	case nil:
		return "", nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
}
