package protocol

// Go ↔ acat-video helper IPC: newline-delimited JSON over the helper's
// stdin (commands) and stdout (events). Every message has "type". The
// helper's first line is VideoReady; the client refuses a helper whose
// Proto differs from VideoVersion (they always ship together).
// The JavaScript side mirrors these names in video/src/protocol.js.

// Commands (client → helper).
const (
	VideoInit         = "init"         // VideoCommand{Token, DisplayName}
	VideoRefreshToken = "refreshToken" // VideoCommand{Token}
	VideoCall         = "call"         // VideoCommand{Participants, Video}
	VideoJoin         = "join"         // VideoCommand{MeetingLink, Video}
	VideoAccept       = "accept"       // VideoCommand{CallID, Video}
	VideoReject       = "reject"       // VideoCommand{CallID}
	VideoHangup       = "hangup"       // VideoCommand{CallID}
	VideoMute         = "mute"         // VideoCommand{CallID, On}
	VideoCamera       = "camera"       // VideoCommand{CallID, On}
	VideoScreenShare  = "screenshare"  // VideoCommand{CallID, On}
	VideoDevices      = "devices"      // → VideoEvent{Type: devices}
	VideoSetDevice    = "setDevice"    // VideoCommand{Kind, DeviceID}
	VideoShow         = "show"
	VideoHide         = "hide"
	VideoQuit         = "quit"
)

// Events (helper → client).
const (
	VideoReady        = "ready"        // VideoEvent{Proto, Version}
	VideoInitialized  = "initialized"  // CallAgent created, incoming calls possible
	VideoIncomingCall = "incomingCall" // VideoEvent{CallID, Caller}
	VideoCallState    = "callState"    // VideoEvent{CallID, State, Reason}
	VideoDeviceList   = "devices"      // VideoEvent{Microphones, Cameras, Speakers}
	VideoTokenExpired = "tokenExpired" // client must send refreshToken
	VideoError        = "error"        // VideoEvent{Message, Command}
)

// Call states (subset of ACS CallState).
const (
	CallConnecting    = "Connecting"
	CallRinging       = "Ringing"
	CallConnected     = "Connected"
	CallLocalHold     = "LocalHold"
	CallRemoteHold    = "RemoteHold"
	CallDisconnecting = "Disconnecting"
	CallDisconnected  = "Disconnected"
)

// Device kinds for VideoSetDevice.
const (
	DeviceMicrophone = "microphone"
	DeviceCamera     = "camera"
	DeviceSpeaker    = "speaker"
)

// Participant is a callee. For Teams users, TeamsUserID is the Entra
// object ID (ACS MicrosoftTeamsUserIdentifier).
type Participant struct {
	TeamsUserID string `json:"teamsUserId"`
}

// VideoCommand is sent to the helper.
type VideoCommand struct {
	Type         string        `json:"type"`
	Proto        int           `json:"proto,omitempty"`
	Token        string        `json:"token,omitempty"`
	DisplayName  string        `json:"displayName,omitempty"`
	CallID       string        `json:"callId,omitempty"`
	Participants []Participant `json:"participants,omitempty"`
	MeetingLink  string        `json:"meetingLink,omitempty"`
	Video        bool          `json:"video,omitempty"`
	On           bool          `json:"on,omitempty"`
	Kind         string        `json:"kind,omitempty"`
	DeviceID     string        `json:"deviceId,omitempty"`
}

// Device is an audio/video device.
type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Caller identifies who is calling.
type Caller struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// CallEndReason mirrors ACS callEndReason.
type CallEndReason struct {
	Code    int `json:"code"`
	SubCode int `json:"subCode"`
}

// VideoEvent is received from the helper.
type VideoEvent struct {
	Type        string         `json:"type"`
	Proto       int            `json:"proto,omitempty"`
	Version     string         `json:"version,omitempty"`
	CallID      string         `json:"callId,omitempty"`
	State       string         `json:"state,omitempty"`
	Reason      *CallEndReason `json:"reason,omitempty"`
	Caller      *Caller        `json:"caller,omitempty"`
	Microphones []Device       `json:"microphones,omitempty"`
	Cameras     []Device       `json:"cameras,omitempty"`
	Speakers    []Device       `json:"speakers,omitempty"`
	Message     string         `json:"message,omitempty"`
	Command     string         `json:"command,omitempty"`
}
