package protocol

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNegotiate(t *testing.T) {
	tests := []struct {
		name             string
		peer, lmin, lmax int
		want             int
		wantErr          bool
	}{
		{"equal", 2, 1, 2, 2, false},
		{"peer older but supported", 1, 1, 2, 1, false},
		{"peer newer", 3, 1, 2, 2, false},
		{"peer too old", 1, 2, 3, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Negotiate(tt.peer, tt.lmin, tt.lmax)
			if tt.wantErr {
				if !errors.Is(err, ErrUnsupportedVersion) {
					t.Fatalf("want ErrUnsupportedVersion, got %v", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("Negotiate(%d,%d,%d) = %d, %v; want %d", tt.peer, tt.lmin, tt.lmax, got, err, tt.want)
			}
		})
	}
}

func TestCompatInvariant(t *testing.T) {
	if RelayMinVersion < RelayVersion-1 {
		t.Fatalf("relay must support N-1: min=%d current=%d", RelayMinVersion, RelayVersion)
	}
	if RelayMinVersion > RelayVersion {
		t.Fatalf("min version %d above current %d", RelayMinVersion, RelayVersion)
	}
}

func TestWireFormat(t *testing.T) {
	b, err := json.Marshal(Welcome{Type: TypeWelcome, Proto: 1, RoutingToken: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"type":"welcome","proto":1,"routingToken":"abc"}`
	if string(b) != want {
		t.Fatalf("got %s, want %s", b, want)
	}
}
