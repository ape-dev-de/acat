package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetPresencesBatches(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1.0/communications/getPresencesByUserId" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var body struct{ IDs []string }
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		if len(body.IDs) > 650 {
			t.Errorf("batch too large: %d", len(body.IDs))
		}
		var vals []string
		for _, id := range body.IDs {
			vals = append(vals, fmt.Sprintf(`{"id":%q,"availability":"Busy","activity":"InACall"}`, id))
		}
		_, _ = io.WriteString(w, `{"value":[`+strings.Join(vals, ",")+`]}`)
	}))
	ids := make([]string, 700)
	for i := range ids {
		ids[i] = fmt.Sprintf("u%d", i)
	}
	got, err := c.GetPresences(context.Background(), ids)
	if err != nil || len(got) != 700 || got["u699"].Availability != AvailBusy || calls.Load() != 2 {
		t.Fatalf("%d presences, %d calls, %v", len(got), calls.Load(), err)
	}
}

func TestSetAndClearPreferredPresence(t *testing.T) {
	c, cap := captureServer(t, 200, "")
	if err := c.SetPreferredPresence(context.Background(), "me", PreferredPresences[2], 90*time.Minute); err != nil {
		t.Fatal(err)
	}
	if cap.path != "/v1.0/users/me/presence/setUserPreferredPresence" ||
		asJSON(cap.body) != `{"activity":"DoNotDisturb","availability":"DoNotDisturb","expirationDuration":"PT1H30M"}` {
		t.Fatalf("%s %s", cap.path, asJSON(cap.body))
	}
	_ = c.SetPreferredPresence(context.Background(), "me", PreferredPresences[0], 0)
	if _, ok := cap.body["expirationDuration"]; ok {
		t.Fatal("no duration expected")
	}
	if err := c.ClearPreferredPresence(context.Background(), "me"); err != nil || cap.path != "/v1.0/users/me/presence/clearUserPreferredPresence" {
		t.Fatalf("%s %v", cap.path, err)
	}
}

func TestISODuration(t *testing.T) {
	for d, want := range map[time.Duration]string{time.Hour: "PT1H", 8 * time.Hour: "PT8H", 30 * time.Minute: "PT30M", 75 * time.Minute: "PT1H15M", time.Second: "PT1M"} {
		if got := isoDuration(d); got != want {
			t.Errorf("%v: %s want %s", d, got, want)
		}
	}
}
