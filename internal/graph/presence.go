package graph

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Presence availability values.
const (
	AvailAvailable     = "Available"
	AvailAvailableIdle = "AvailableIdle"
	AvailAway          = "Away"
	AvailBeRightBack   = "BeRightBack"
	AvailBusy          = "Busy"
	AvailBusyIdle      = "BusyIdle"
	AvailDoNotDisturb  = "DoNotDisturb"
	AvailOffline       = "Offline"
	AvailUnknown       = "PresenceUnknown"
)

// Presence is a user's Teams presence.
type Presence struct {
	ID           string `json:"id"`
	Availability string `json:"availability"`
	Activity     string `json:"activity"`
}

// maxPresenceBatch is the documented maximum of getPresencesByUserId.
const maxPresenceBatch = 650

// GetPresences returns presence for the given user IDs (batched).
func (c *Client) GetPresences(ctx context.Context, ids []string) (map[string]Presence, error) {
	out := make(map[string]Presence, len(ids))
	for start := 0; start < len(ids); start += maxPresenceBatch {
		batch := ids[start:min(start+maxPresenceBatch, len(ids))]
		body := struct {
			IDs []string `json:"ids"`
		}{batch}
		var p page[Presence]
		if err := c.do(ctx, request{method: http.MethodPost, path: "/communications/getPresencesByUserId", body: body}, &p); err != nil {
			return out, err
		}
		for _, pr := range p.Value {
			out[pr.ID] = pr
		}
	}
	return out, nil
}

// PreferredPresence is a status the user can choose.
type PreferredPresence struct {
	Label        string
	Availability string
	Activity     string
}

// PreferredPresences are the valid availability/activity combinations for
// setUserPreferredPresence.
var PreferredPresences = []PreferredPresence{
	{"Available", AvailAvailable, "Available"},
	{"Busy", AvailBusy, "Busy"},
	{"Do not disturb", AvailDoNotDisturb, "DoNotDisturb"},
	{"Be right back", AvailBeRightBack, "BeRightBack"},
	{"Appear away", AvailAway, "Away"},
	{"Appear offline", AvailOffline, "OffWork"},
}

// SetPreferredPresence sets the user's preferred presence for d (Graph
// default 7 days if d is 0).
func (c *Client) SetPreferredPresence(ctx context.Context, userID string, p PreferredPresence, d time.Duration) error {
	body := map[string]string{"availability": p.Availability, "activity": p.Activity}
	if d > 0 {
		body["expirationDuration"] = isoDuration(d)
	}
	path := "/users/" + url.PathEscape(userID) + "/presence/setUserPreferredPresence"
	return c.do(ctx, request{method: http.MethodPost, path: path, body: body}, nil)
}

// ClearPreferredPresence returns to automatic presence.
func (c *Client) ClearPreferredPresence(ctx context.Context, userID string) error {
	path := "/users/" + url.PathEscape(userID) + "/presence/clearUserPreferredPresence"
	return c.do(ctx, request{method: http.MethodPost, path: path, body: struct{}{}}, nil)
}

// isoDuration formats d as ISO 8601 (PT#H#M), minute precision.
func isoDuration(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	h, m := m/60, m%60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("PT%dH%dM", h, m)
	case h > 0:
		return fmt.Sprintf("PT%dH", h)
	default:
		return fmt.Sprintf("PT%dM", max(m, 1))
	}
}
