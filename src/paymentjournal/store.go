// Package paymentjournal provides a single-writer durable operation journal.
// Submitted operations recovered after a process exit become Unknown. The
// journal never performs payments or grants access; callers reconcile outcomes.
package paymentjournal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Direction string

const (
	Incoming Direction = "incoming"
	Outgoing Direction = "outgoing"
)

type State string

const (
	Prepared     State = "prepared"
	Submitted    State = "submitted"
	Unknown      State = "unknown"
	Awaiting     State = "awaiting"
	Paid         State = "paid"
	GrantPending State = "grant_pending"
	Completed    State = "completed"
	Failed       State = "failed"
)

type Operation struct {
	ID             string    `json:"id"`
	WalletID       string    `json:"wallet_id"`
	Direction      Direction `json:"direction"`
	AmountMSat     uint64    `json:"amount_msat"`
	FeeReserveMSat uint64    `json:"fee_reserve_msat"`
	ActualFeeMSat  uint64    `json:"actual_fee_msat"`
	PaymentHash    string    `json:"payment_hash,omitempty"`
	Recipient      string    `json:"recipient"`
	Request        []byte    `json:"request,omitempty"` // Prepared encrypted event or nonsecret invoice request; never credentials.
	Invoice        []byte    `json:"invoice,omitempty"`
	State          State     `json:"state"`
	Version        uint64    `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type Budget struct {
	LimitMSat uint64
	Since     time.Time
}
type snapshot struct {
	Format     int                  `json:"format"`
	Operations map[string]Operation `json:"operations"`
}
type Store struct {
	mu               sync.Mutex
	dir              string
	lock             *os.File
	data             snapshot
	digest           [32]byte
	poisoned, closed bool
	rename           func(string, string) error
}

var ErrRefused = errors.New("payment journal refused operation")

const (
	maxBytes   = 4 << 20
	maxRecords = 1024
	maxPayload = 64 << 10
)

func validID(s string) bool {
	if len(s) == 0 || len(s) > 256 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-_.:@", r)) {
			return false
		}
	}
	return true
}
func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func known(s State) bool {
	switch s {
	case Prepared, Submitted, Unknown, Awaiting, Paid, GrantPending, Completed, Failed:
		return true
	}
	return false
}
func valid(o Operation) bool {
	if !validID(o.ID) || !validID(o.WalletID) || !validID(o.Recipient) || o.AmountMSat == 0 || o.AmountMSat > math.MaxInt64 || o.FeeReserveMSat > math.MaxInt64 || o.ActualFeeMSat > math.MaxInt64 || len(o.Request) > maxPayload || len(o.Invoice) > maxPayload {
		return false
	}
	if o.Direction != Incoming && o.Direction != Outgoing {
		return false
	}
	if o.Direction == Outgoing && !validHash(o.PaymentHash) {
		return false
	}
	if o.Direction == Incoming && (o.FeeReserveMSat != 0 || (o.PaymentHash != "" && !validHash(o.PaymentHash))) {
		return false
	}
	return true
}
func protected(path string, dir bool) error {
	st, e := os.Lstat(path)
	if e != nil {
		return ErrRefused
	}
	expected := os.FileMode(0600)
	if dir {
		expected = 0700
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || sys.Uid != uint32(os.Geteuid()) || (!dir && sys.Nlink != 1) || st.Mode().Perm() != expected || st.Mode()&os.ModeSymlink != 0 || st.IsDir() != dir || (!dir && !st.Mode().IsRegular()) {
		return ErrRefused
	}
	return nil
}
func noSymlinks(path string) error {
	clean, e := filepath.Abs(path)
	if e != nil {
		return ErrRefused
	}
	for p := clean; ; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e != nil {
			return ErrRefused
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return ErrRefused
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}
func Open(dir string) (*Store, error) {
	abs, e := filepath.Abs(dir)
	if e != nil {
		return nil, ErrRefused
	}
	if e = os.Mkdir(abs, 0700); e != nil && !os.IsExist(e) {
		return nil, ErrRefused
	}
	if noSymlinks(abs) != nil || protected(abs, true) != nil {
		return nil, ErrRefused
	}
	fd, e := syscall.Open(filepath.Join(abs, "writer.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, ErrRefused
	}
	f := os.NewFile(uintptr(fd), "payment-journal-lock")
	if protected(filepath.Join(abs, "writer.lock"), false) != nil || syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, ErrRefused
	}
	s := &Store{dir: abs, lock: f, data: snapshot{Format: 1, Operations: map[string]Operation{}}, rename: os.Rename}
	fail := func() (*Store, error) { f.Close(); return nil, ErrRefused }
	path := filepath.Join(abs, "operations.json")
	_, e = os.Lstat(path)
	if os.IsNotExist(e) {
		if e = s.persist(s.data, true); e != nil {
			return fail()
		}
		return s, nil
	}
	if e != nil || protected(path, false) != nil {
		return fail()
	}
	b, e := readBounded(path)
	if e != nil || strictJSON(b) != nil {
		return fail()
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&s.data) != nil {
		return fail()
	}
	var more any
	if decoder.Decode(&more) != io.EOF || s.data.Format != 1 || s.data.Operations == nil || len(s.data.Operations) > maxRecords {
		return fail()
	}
	changed := false
	for id, o := range s.data.Operations {
		if id != o.ID || !valid(o) || !validRecovered(o) || o.CreatedAt.IsZero() || o.UpdatedAt.Before(o.CreatedAt) {
			return fail()
		}
		if o.State == Submitted {
			o.State = Unknown
			o.Version++
			o.UpdatedAt = time.Now().UTC()
			s.data.Operations[id] = o
			changed = true
		}
	}
	s.digest = sha256.Sum256(b)
	if changed {
		if e = s.persist(s.data, false); e != nil {
			return fail()
		}
	}
	return s, nil
}
func readBounded(path string) ([]byte, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, ErrRefused
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if e != nil || len(b) > maxBytes {
		return nil, ErrRefused
	}
	return b, nil
}
func (s *Store) ready() error {
	if s.closed || s.poisoned {
		return ErrRefused
	}
	path := filepath.Join(s.dir, "operations.json")
	if protected(path, false) != nil {
		return ErrRefused
	}
	b, e := readBounded(path)
	if e != nil || sha256.Sum256(b) != s.digest {
		s.poisoned = true
		return ErrRefused
	}
	return nil
}
func (s *Store) persist(data snapshot, initial bool) error {
	if !initial && s.ready() != nil {
		return ErrRefused
	}
	b, e := json.Marshal(data)
	if e != nil || len(b) > maxBytes {
		return ErrRefused
	}
	f, e := os.CreateTemp(s.dir, ".payment-journal-")
	if e != nil {
		s.poisoned = true
		return ErrRefused
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e == nil {
		e = s.rename(tmp, filepath.Join(s.dir, "operations.json"))
	}
	if e == nil {
		var d *os.File
		d, e = os.Open(s.dir)
		if e == nil {
			e = d.Sync()
			d.Close()
		}
	}
	if e != nil {
		s.poisoned = true
		return ErrRefused
	}
	s.digest = sha256.Sum256(b)
	s.data = data
	return nil
}
func clone(o Operation) Operation {
	o.Request = append([]byte(nil), o.Request...)
	o.Invoice = append([]byte(nil), o.Invoice...)
	return o
}
func (s *Store) copyData() snapshot {
	d := snapshot{Format: 1, Operations: make(map[string]Operation, len(s.data.Operations))}
	for k, v := range s.data.Operations {
		d.Operations[k] = clone(v)
	}
	return d
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.lock.Close()
}
func (s *Store) Get(id string) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready() != nil {
		return Operation{}, ErrRefused
	}
	o, ok := s.data.Operations[id]
	if !ok {
		return Operation{}, ErrRefused
	}
	return clone(o), nil
}
func (s *Store) List() ([]Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready() != nil {
		return nil, ErrRefused
	}
	out := make([]Operation, 0, len(s.data.Operations))
	for _, o := range s.data.Operations {
		out = append(out, clone(o))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func add(a, b uint64) (uint64, error) {
	if b > math.MaxUint64-a {
		return 0, ErrRefused
	}
	return a + b, nil
}
func (s *Store) Prepare(o Operation, budget Budget) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready() != nil || !valid(o) || o.State != "" || o.Version != 0 || o.ActualFeeMSat != 0 || !o.CreatedAt.IsZero() || !o.UpdatedAt.IsZero() || len(o.Invoice) != 0 {
		return Operation{}, ErrRefused
	}
	if old, ok := s.data.Operations[o.ID]; ok {
		compare := clone(old)
		compare.State = ""
		compare.Version = 0
		compare.ActualFeeMSat = 0
		compare.CreatedAt = time.Time{}
		compare.UpdatedAt = time.Time{}
		compare.Invoice = nil
		if compare.Direction == Incoming && o.PaymentHash == "" {
			compare.PaymentHash = ""
		}
		if !reflect.DeepEqual(compare, o) {
			return Operation{}, ErrRefused
		}
		return clone(old), nil
	}
	if len(s.data.Operations) >= maxRecords {
		return Operation{}, ErrRefused
	}
	if o.Direction == Outgoing {
		total, e := add(o.AmountMSat, o.FeeReserveMSat)
		if e != nil {
			return Operation{}, e
		}
		for _, old := range s.data.Operations {
			if old.WalletID != o.WalletID || old.Direction != Outgoing || old.State == Failed {
				continue
			}
			resolved := old.State == Paid || old.State == Completed
			if resolved && !budget.Since.IsZero() && old.CreatedAt.Before(budget.Since) {
				continue
			}
			fee := old.FeeReserveMSat
			if resolved {
				fee = old.ActualFeeMSat
			}
			cost, e := add(old.AmountMSat, fee)
			if e != nil {
				return Operation{}, e
			}
			total, e = add(total, cost)
			if e != nil {
				return Operation{}, e
			}
		}
		if total > budget.LimitMSat {
			return Operation{}, ErrRefused
		}
	}
	o.State = Prepared
	o.Version = 1
	o.CreatedAt = time.Now().UTC()
	o.UpdatedAt = o.CreatedAt
	d := s.copyData()
	d.Operations[o.ID] = clone(o)
	if e := s.persist(d, false); e != nil {
		return Operation{}, e
	}
	return clone(o), nil
}
func allowed(o Operation, next State) bool {
	switch o.State {
	case Prepared:
		return next == Submitted || next == Failed
	case Submitted:
		return next == Unknown || next == Failed || (o.Direction == Outgoing && next == Paid)
	case Unknown:
		return next == Failed || (next == Paid && (o.Direction == Outgoing || (validHash(o.PaymentHash) && len(o.Invoice) > 0)))
	case Awaiting:
		return o.Direction == Incoming && (next == Paid || next == Unknown || next == Failed)
	case Paid:
		if o.Direction == Outgoing {
			return next == Completed
		}
		return next == GrantPending
	case GrantPending:
		return o.Direction == Incoming && next == Completed
	}
	return false
}
func (s *Store) Advance(id string, version uint64, next State, fee uint64) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready() != nil {
		return Operation{}, ErrRefused
	}
	o, ok := s.data.Operations[id]
	if !ok || o.Version != version || version == math.MaxUint64 || !allowed(o, next) || fee > math.MaxInt64 || (next != Paid && fee != 0) || (o.Direction == Incoming && fee != 0) {
		return Operation{}, ErrRefused
	}
	o.State = next
	o.Version++
	o.UpdatedAt = time.Now().UTC()
	if next == Paid {
		o.ActualFeeMSat = fee
	}
	d := s.copyData()
	d.Operations[id] = o
	if e := s.persist(d, false); e != nil {
		return Operation{}, e
	}
	return clone(o), nil
}
func (s *Store) BindInvoice(id string, version uint64, hash string, invoice []byte) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready() != nil || !validHash(hash) || len(invoice) == 0 || len(invoice) > maxPayload {
		return Operation{}, ErrRefused
	}
	o, ok := s.data.Operations[id]
	if !ok || o.Direction != Incoming || o.Version != version || version == math.MaxUint64 || (o.State != Submitted && o.State != Unknown) || o.PaymentHash != "" {
		return Operation{}, ErrRefused
	}
	o.PaymentHash = hash
	o.Invoice = append([]byte(nil), invoice...)
	o.State = Awaiting
	o.Version++
	o.UpdatedAt = time.Now().UTC()
	d := s.copyData()
	d.Operations[id] = o
	if e := s.persist(d, false); e != nil {
		return Operation{}, e
	}
	return clone(o), nil
}

// Reject duplicate keys before decoding maps: silently keeping the last value
// could discard a reserved or paid operation from an otherwise valid snapshot.
func strictJSON(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	var value func(int) error
	value = func(depth int) error {
		if depth > 16 {
			return ErrRefused
		}
		token, e := d.Token()
		if e != nil {
			return ErrRefused
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return ErrRefused
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return ErrRefused
				}
				seen[key] = true
				if value(depth+1) != nil {
					return ErrRefused
				}
			}
		case '[':
			for d.More() {
				if value(depth+1) != nil {
					return ErrRefused
				}
			}
		default:
			return ErrRefused
		}
		_, e = d.Token()
		if e != nil {
			return ErrRefused
		}
		return nil
	}
	if value(0) != nil {
		return ErrRefused
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrRefused
	}
	return nil
}
func validRecovered(o Operation) bool {
	if !known(o.State) || o.Version == 0 || (o.State == Submitted && o.Version == math.MaxUint64) {
		return false
	}
	if o.Direction == Outgoing && (o.State == Awaiting || o.State == GrantPending) {
		return false
	}
	if o.Direction == Incoming {
		if o.ActualFeeMSat != 0 {
			return false
		}
		switch o.State {
		case Awaiting, Paid, GrantPending, Completed:
			if !validHash(o.PaymentHash) || len(o.Invoice) == 0 {
				return false
			}
		}
	}
	if o.State != Paid && o.State != Completed && o.ActualFeeMSat != 0 {
		return false
	}
	return true
}
