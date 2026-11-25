// Built-in multi-step portal recipes.
//
// These are operator-selectable by id through
// `upstream_wifi.portal_login_recipes[].recipe`. They are deliberately small
// and inspectable; a recipe that encodes a payment flow or an identity reset
// does not belong here.
package wireless_gateway_manager

// BuiltinPortalRecipes returns the recipes shipped with this module. The map
// is copied on every call so a caller can add runtime seed variables without
// mutating the next caller's recipe.
func BuiltinPortalRecipes() map[string]PortalRecipeSpec {
	return map[string]PortalRecipeSpec{
		"man-purple-free": manPurpleFreeRecipe(),
		"ask4-guest":      ask4GuestRecipe(),
	}
}

// manPurpleFreeRecipe is the Manchester Airport Purple/Aruba flow:
//
//	Aruba redirect -> Purple page (JS adds jsrd=1) -> email sign-in ->
//	free plan 739 -> /access/validate -> controller cmd=authenticate ->
//	controller meta-refresh welcome page.
//
// `start_url` must be the Aruba redirect URL (for example
// http://1.1.1.1/?cmd=redirect&arubalp=12345); the operator sets it from the
// venue's intercept. The controller and portal hosts are allow-listed, so the
// cross-host redirect is permitted but nothing else is.
func manPurpleFreeRecipe() PortalRecipeSpec {
	return PortalRecipeSpec{
		ID:            "man-purple-free",
		Name:          "Manchester Airport Purple free plan",
		SchemaVersion: 1,
		Credentials:   false,
		AllowHosts:    []string{"purpleportal.net", "10.234.240.41"},
		Flow: PortalRecipeFlow{Type: "http-api", Steps: []PortalRecipeStep{
			{
				Type:       "request",
				URL:        "{{start_url}}",
				NoRedirect: true,
				HeaderVars: map[string]string{"portal_url": "Location"},
			},
			{
				Type: "request",
				URL:  "{{portal_url}}",
			},
			{
				Type:     "request",
				URL:      "{{current_url}}&jsrd=1",
				HTMLVars: map[string]string{"csrf": `name="csrf_token" value="([^"]+)"`},
			},
			{
				Type:   "request",
				Method: "POST",
				URL:    "{{origin}}/access/auth",
				Form: map[string]string{
					"email":      "{{user}}",
					"form_type":  "LoginForm",
					"csrf_token": "{{csrf}}",
				},
			},
			{
				Type: "attempts",
				Candidates: [][]PortalRecipeStep{
					{
						{
							Type:   "request",
							Method: "POST",
							URL:    "{{origin}}/access/paid-bandwidth/purchase",
							Form:   map[string]string{"form_type": "", "plan": "739"},
						},
						{
							Type:       "request",
							URL:        "{{origin}}/access/validate",
							NoRedirect: true,
							HeaderVars: map[string]string{"auth_url": "Location"},
						},
					},
					{
						{
							Type:       "request",
							URL:        "{{origin}}/access/validate",
							NoRedirect: true,
							HeaderVars: map[string]string{"auth_url": "Location"},
						},
					},
				},
			},
			{
				Type:     "request",
				URL:      "{{auth_url}}",
				HTMLVars: map[string]string{"next": `(?is)url=([^"']+)`},
			},
			{
				Type: "request",
				URL:  "{{next}}",
			},
		}},
	}
}

// ask4GuestRecipe is the ASK4 guest form: first name + surname + CSRF, no
// account password. `start_url` must be the `/en/guest-signup` page.
func ask4GuestRecipe() PortalRecipeSpec {
	return PortalRecipeSpec{
		ID:            "ask4-guest",
		Name:          "ASK4 guest login",
		SchemaVersion: 1,
		AllowHosts:    []string{"signup.ask4.com", "gw.customer.ask4.lan:3145"},
		Flow: PortalRecipeFlow{Type: "http-api", Steps: []PortalRecipeStep{
			{
				Type:     "request",
				URL:      "{{start_url}}",
				HTMLVars: map[string]string{"csrf": `name="personal_details\[_token\]" value="([^"]+)"`},
			},
			{
				Type:   "request",
				Method: "POST",
				URL:    "{{origin}}/en/guest-signup",
				Form: map[string]string{
					"personal_details[firstname]": "TollGate",
					"personal_details[surname]":   "Operator",
					"personal_details[_token]":    "{{csrf}}",
				},
			},
		}},
	}
}
