package recording

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Runs the real pinned Karte receiver and policy engine in a synthetic root．
// No model，raw audio，user content or production registration is involved．
func TestInstalledKarteDeliveryRestartReceiptReadbackAndRevocation(t *testing.T) {
	control := os.Getenv("EPHY_TEST_KARTE_CONTROL")
	if control == "" {
		t.Skip("set EPHY_TEST_KARTE_CONTROL to the pinned Karte control binary")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	data := filepath.Join(base, "karte")
	config := filepath.Join(base, "config")
	if e := os.Mkdir(data, 0700); e != nil {
		t.Fatal(e)
	}
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command(control, append([]string{"-data-root", data, "-config-root", config}, args...)...)
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("control: %v %s", e, b)
		}
		return b
	}
	run("process")
	o := Options{Home: filepath.Join(base, "queue"), DataRoot: data, ControlPath: control, KarteConfigRoot: config, Prepare: func(context.Context, string) error { return nil }}
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	// Match the native 500 ms processor cadence．Registration alone does not
	// publish capabilities，so ON must wait before the first event can run．
	advertised := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(o.Home, "producer.json")); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
		advertised <- exec.Command(control, "-data-root", data, "-config-root", config, "process").Run()
	}()
	status, e := s.Configure(context.Background(), ConfigureRequest{DataRoot: data, Project: "synthetic", Timezone: "Asia/Tokyo", Enabled: true})
	if e != nil || !status.Settings.Enabled {
		t.Fatal(e)
	}
	root, e := os.OpenRoot(data)
	if e != nil {
		t.Fatal(e)
	}
	_, e = loadCapabilities(root, status.Settings)
	root.Close()
	if e != nil {
		t.Fatal("ON returned before native grant advertisement", e)
	}
	if e = <-advertised; e != nil {
		t.Fatal(e)
	}
	conv := status.Settings.ConversationID
	for i := 0; i < 32; i++ {
		key, e := s.Begin(uuid.NewString(), conv, "synthetic user", "text", nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.Checkpoint(key, "synthetic assistant", completed(), true); e != nil {
			t.Fatal(e)
		}
		if e = s.Finish(key, completed(), true); e != nil {
			t.Fatal(e)
		}
	}
	// Offline publication retains stable proposal bytes and never reports saved．
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.DispatchOnce(ctx)
	if s.Snapshot().Saved != 0 || s.Snapshot().Local != 64 {
		t.Fatal("offline falsely saved")
	}
	s.Close()
	s, e = New(o)
	if e != nil {
		t.Fatal(e)
	}
	// Receiver commits while producer is down，then producer starts from its
	// durable candidate and obtains receipt plus authorized readback．
	run("process")
	processorCtx, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-processorCtx.Done():
				return
			case <-time.After(20 * time.Millisecond):
				cmd := exec.Command(control, "-data-root", data, "-config-root", config, "process")
				if e := cmd.Run(); e != nil {
					return
				}
			}
		}
	}()
	defer func() { stop(); wg.Wait() }()
	for i := 0; i < 80 && s.Snapshot().Local > 0; i++ {
		if !s.DispatchOnce(context.Background()) {
			t.Fatal("delivery stalled", s.Snapshot())
		}
	}
	st := s.Snapshot()
	if st.Saved != 64 || st.Local != 0 || st.Failed != 0 {
		t.Fatalf("wrong coverage: %+v", st)
	}
	target := st.Records[0].Target
	r, e := s.Read(context.Background(), target)
	if e != nil || len(r.Events) != 64 {
		t.Fatal("readback", e)
	}
	seen := map[string]bool{}
	for i, event := range r.Events {
		if seen[event.EventID] || event.Seq != int64(i+1) {
			t.Fatal("duplicate or sequence gap")
		}
		seen[event.EventID] = true
	}
	if len(s.turns) != 0 {
		t.Fatal("acknowledged body not purged")
	}
	// An exact read remains policy governed after restart and grant revocation．
	s.Close()
	s, e = New(o)
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(o.Home, "grant.json"))
	if e != nil {
		t.Fatal(e)
	}
	var grant map[string]any
	if e = json.Unmarshal(b, &grant); e != nil {
		t.Fatal(e)
	}
	grant["enabled"] = false
	grant["policy_revision"] = 2
	grant["consent_epoch"] = 2
	b, _ = json.Marshal(grant)
	grantPath := filepath.Join(base, "revoked.json")
	os.WriteFile(grantPath, b, 0600)
	run("-grant", grantPath, "-producer-credential", filepath.Join(o.Home, "producer.json"), "configure")
	run("process")
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, e = s.Read(ctx, target); e == nil {
		t.Fatal("revoked content disclosed")
	}
	if _, e = s.Configure(ctx, ConfigureRequest{DataRoot: data, Project: "synthetic", Timezone: "Asia/Tokyo", Enabled: true}); e == nil {
		t.Fatal("revoked grant silently re-enabled")
	}
}
