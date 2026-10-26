package multi_wan

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

const testBackup = "/backups/mwan3.20261026T120000.000000123Z.bak"

var errApplyTest = errors.New("injected failure")

type applyRecorder struct {
	files map[string][]byte
	calls []string
	fail  string
}

func (r *applyRecorder) record(call string) error {
	r.calls = append(r.calls, call)
	if call == r.fail {
		return errApplyTest
	}
	return nil
}
func (r *applyRecorder) Run(_ context.Context, name string, args ...string) (string, error) {
	return "", r.record("run " + name + " " + strings.Join(args, " "))
}
func (r *applyRecorder) ReadFile(_ context.Context, path string) ([]byte, error) {
	if err := r.record("read " + path); err != nil {
		return nil, err
	}
	b, ok := r.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), b...), nil
}
func (r *applyRecorder) WriteFile(_ context.Context, path string, data []byte, mode os.FileMode) error {
	if err := r.record("write " + path); err != nil {
		return err
	}
	if mode != 0600 && mode != 0644 {
		return errors.New("unexpected mode")
	}
	r.files[path] = append([]byte(nil), data...)
	return nil
}
func applyFixture() (*Applier, *applyRecorder, []Outcome) {
	r := &applyRecorder{files: map[string][]byte{"/etc/config/mwan3": []byte("previous")}}
	return &Applier{Run: r, BackupDir: "/backups", Now: func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 123, time.UTC) }}, r, []Outcome{{Weights: map[string]int{"a": 50, "b": 50}}}
}
func TestApplyIdenticalNoOp(t *testing.T) {
	a, r, o := applyFixture()
	c := fixture()
	text, err := RenderMWAN3(c, o, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.files["/etc/config/mwan3"] = []byte(text)
	result, err := a.Apply(context.Background(), c, o, nil)
	if err != nil || result.Changed || result.MWAN3Restarted || result.BackupPath != "" || result.MarksFragmentPath != "/etc/nftables.d/50-multiwan-marks.nft" {
		t.Fatalf("%+v %v", result, err)
	}
	if !reflect.DeepEqual(r.calls, []string{"read /etc/config/mwan3"}) {
		t.Fatal(r.calls)
	}
}
func expectedApplyCalls() []string {
	return []string{"read /etc/config/mwan3", "run mkdir -p -m 0700 /backups", "run chmod 0700 /backups", "read " + testBackup, "write " + testBackup, "write /etc/config/mwan3", "run uci commit mwan3", "run /etc/init.d/mwan3 restart", "read /etc/nftables.d/50-multiwan-marks.nft", "write /etc/nftables.d/50-multiwan-marks.nft", "run fw4 reload"}
}
func TestApplyChangedOrderAndNoRuntimeNFT(t *testing.T) {
	a, r, o := applyFixture()
	c := fixture()
	result, err := a.Apply(context.Background(), c, o, nil)
	if err != nil || !result.Changed || !result.MWAN3Restarted || result.BackupPath != testBackup {
		t.Fatalf("%+v %v", result, err)
	}
	if !reflect.DeepEqual(r.calls, expectedApplyCalls()) {
		t.Fatalf("calls: %v", r.calls)
	}
	if string(r.files[testBackup]) != "previous" {
		t.Fatal("backup lost")
	}
	want, _ := RenderMWAN3(c, o, nil)
	marks, _ := RenderMarks(c, o)
	if string(r.files["/etc/config/mwan3"]) != want || string(r.files[result.MarksFragmentPath]) != marks {
		t.Fatal("wrong artifacts")
	}
	for _, call := range r.calls {
		if strings.Contains(call, "nft add") || strings.Contains(call, "nft insert") {
			t.Fatal(call)
		}
	}
}
func TestApplyStopsAtEveryIOFailure(t *testing.T) {
	for i, call := range expectedApplyCalls() {
		t.Run(call, func(t *testing.T) {
			a, r, o := applyFixture()
			r.fail = call
			result, err := a.Apply(context.Background(), fixture(), o, nil)
			if !errors.Is(err, errApplyTest) {
				t.Fatalf("%+v %v", result, err)
			}
			if !reflect.DeepEqual(r.calls, expectedApplyCalls()[:i+1]) {
				t.Fatal(r.calls)
			}
			if i <= 4 && string(r.files["/etc/config/mwan3"]) != "previous" {
				t.Fatal("backup failure overwrote current config")
			}
			if i >= 5 && result.BackupPath != testBackup {
				t.Fatal("lost recovery handle")
			}
		})
	}
}
func TestApplyRefusesBeforeIO(t *testing.T) {
	for _, kind := range []string{"nil", "disabled", "invalid", "render"} {
		t.Run(kind, func(t *testing.T) {
			a, r, o := applyFixture()
			c := fixture()
			switch kind {
			case "nil":
				c = nil
			case "disabled":
				c.Enabled = false
			case "invalid":
				c.Engine = "invalid"
			case "render":
				o = nil
			}
			if _, err := a.Apply(context.Background(), c, o, nil); err == nil {
				t.Fatal("accepted")
			}
			if len(r.calls) != 0 {
				t.Fatal(r.calls)
			}
		})
	}
}
func TestApplyUnchangedFragmentDoesNotReload(t *testing.T) {
	a, r, o := applyFixture()
	c := fixture()
	marks, _ := RenderMarks(c, o)
	r.files["/etc/nftables.d/50-multiwan-marks.nft"] = []byte(marks)
	if _, err := a.Apply(context.Background(), c, o, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.calls, expectedApplyCalls()[:9]) {
		t.Fatal(r.calls)
	}
}
func TestApplyRefusesBackupCollision(t *testing.T) {
	a, r, o := applyFixture()
	r.files[testBackup] = []byte("older backup")
	if _, err := a.Apply(context.Background(), fixture(), o, nil); err == nil {
		t.Fatal("overwrote backup")
	}
	if !reflect.DeepEqual(r.calls, expectedApplyCalls()[:4]) {
		t.Fatal(r.calls)
	}
}
func TestRestore(t *testing.T) {
	want := []string{"read " + testBackup, "write /etc/config/mwan3", "run uci commit mwan3", "run /etc/init.d/mwan3 restart"}
	for i := -1; i < len(want); i++ {
		a, r, _ := applyFixture()
		r.files[testBackup] = []byte("restore me")
		if i >= 0 {
			r.fail = want[i]
		}
		err := a.Restore(context.Background(), testBackup, "")
		if i < 0 {
			if err != nil || string(r.files["/etc/config/mwan3"]) != "restore me" {
				t.Fatal(err)
			}
		} else if !errors.Is(err, errApplyTest) {
			t.Fatal(err)
		}
		n := len(want)
		if i >= 0 {
			n = i + 1
		}
		if !reflect.DeepEqual(r.calls, want[:n]) {
			t.Fatal(r.calls)
		}
	}
}

func TestApplyBacksUpExistingMarksFragmentAndRestoreReinstallsIt(t *testing.T) {
	a, r, o := applyFixture()
	r.files[marksFragmentPath] = []byte("old-fragment")
	res, err := a.Apply(context.Background(), fixture(), o, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	wantMarks := "/backups/marks.20261026T120000.000000123Z.bak"
	if res.MarksBackupPath != wantMarks {
		t.Fatalf("marks backup = %q, want %q", res.MarksBackupPath, wantMarks)
	}
	if string(r.files[wantMarks]) != "old-fragment" {
		t.Fatalf("marks backup content = %q", r.files[wantMarks])
	}
	r.calls = nil
	if err := a.Restore(context.Background(), res.BackupPath, res.MarksBackupPath); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if string(r.files[marksFragmentPath]) != "old-fragment" {
		t.Fatalf("fragment not restored: %q", r.files[marksFragmentPath])
	}
	joined := strings.Join(r.calls, "\n")
	for _, want := range []string{"uci commit mwan3", "/etc/init.d/mwan3 restart", "write " + marksFragmentPath, "run fw4 reload"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("restore missing %q in calls:\n%s", want, joined)
		}
	}
}
