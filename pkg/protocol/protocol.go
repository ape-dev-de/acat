// Package protocol defines the wire protocol between the acat client and
// acat-relay, and between the acat client and the acat-video helper.
//
// Compatibility rule (see docs/protocol.md): the relay supports protocol
// version N and N-1. New functionality is added as named features
// announced in Welcome.Features; only breaking changes bump the version.
package protocol

import (
	"errors"
	"fmt"
)

// RelayVersion is the current client↔relay protocol version.
const RelayVersion = 1

// RelayMinVersion is the oldest client↔relay protocol version still
// supported by this build. Invariant: RelayMinVersion >= RelayVersion-1.
const RelayMinVersion = 1

// VideoVersion is the current client↔video-helper IPC version. Client and
// helper ship together, so only an exact match is accepted.
const VideoVersion = 1

// Message types on the relay WebSocket.
const (
	TypeHello        = "hello"
	TypeWelcome      = "welcome"
	TypeNotification = "notification"
	TypeLifecycle    = "lifecycle"
	TypeError        = "error"
)

// Hello is the first frame a client sends after the WebSocket is open.
type Hello struct {
	Type   string `json:"type"`   // always TypeHello
	Proto  int    `json:"proto"`  // highest protocol version the client speaks
	Client string `json:"client"` // free-form client identifier, e.g. "acat/0.1.0"
}

// Welcome is the relay's answer to Hello.
type Welcome struct {
	Type         string   `json:"type"`         // always TypeWelcome
	Proto        int      `json:"proto"`        // negotiated protocol version
	RoutingToken string   `json:"routingToken"` // random, valid for this connection only
	Features     []string `json:"features,omitempty"`
}

// ErrUnsupportedVersion is returned when no common protocol version exists.
var ErrUnsupportedVersion = errors.New("protocol: unsupported version")

// Negotiate returns the protocol version to use when a peer speaking up to
// peerMax talks to a side that supports [localMin, localMax].
func Negotiate(peerMax, localMin, localMax int) (int, error) {
	if peerMax < localMin {
		return 0, fmt.Errorf("%w: peer speaks %d, need >= %d", ErrUnsupportedVersion, peerMax, localMin)
	}
	if peerMax > localMax {
		return localMax, nil
	}
	return peerMax, nil
}
