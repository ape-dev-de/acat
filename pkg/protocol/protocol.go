// Package protocol defines the wire protocol between the acat client and
// acat-relay, and between the acat client and the acat-video helper.
//
// Compatibility rule (see docs/protocol.md): the relay supports protocol
// version N and N-1. New functionality is added as named features
// announced in Welcome.Features; only breaking changes bump the version.
// Receivers must ignore unknown fields and features.
package protocol

import (
	"encoding/json"
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
	TypeHello        = "hello"        // client → relay, first frame
	TypeWelcome      = "welcome"      // relay → client, answer to hello
	TypeNotification = "notification" // relay → client, Graph change notification
	TypeLifecycle    = "lifecycle"    // relay → client, Graph lifecycle notification
	TypeError        = "error"        // relay → client, followed by close
)

// Error codes in Error.Code.
const (
	ErrCodeUnsupportedVersion = "unsupported_version"
	ErrCodeTooManyConnections = "too_many_connections"
)

// Features announced in Welcome.Features.
const (
	// FeatureACSToken: POST /acs/token exchanges a Teams user's Entra
	// token (ACS scopes) for an ACS access token.
	FeatureACSToken = "acs-token"
)

// CloseSlowConsumer is the close reason when a client cannot keep up.
const CloseSlowConsumer = "slow consumer"

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

// Push carries a Graph webhook body (TypeNotification or TypeLifecycle)
// unchanged. Graph notifications without resource data contain IDs only.
type Push struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// Error is sent before the relay closes a connection on purpose.
type Error struct {
	Type    string `json:"type"` // always TypeError
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// Envelope is used to peek at the type of an incoming frame.
type Envelope struct {
	Type string `json:"type"`
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
