package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// Subscription is a Graph change-notification subscription.
type Subscription struct {
	ID                       string    `json:"id,omitempty"`
	Resource                 string    `json:"resource"`
	ChangeType               string    `json:"changeType"`
	NotificationURL          string    `json:"notificationUrl"`
	LifecycleNotificationURL string    `json:"lifecycleNotificationUrl,omitempty"`
	ClientState              string    `json:"clientState,omitempty"`
	ExpirationDateTime       time.Time `json:"expirationDateTime"`
	IncludeResourceData      bool      `json:"includeResourceData"`
}

// CreateSubscription creates a subscription and returns it with its ID.
func (c *Client) CreateSubscription(ctx context.Context, s Subscription) (Subscription, error) {
	var out Subscription
	err := c.do(ctx, request{method: http.MethodPost, path: "/subscriptions", body: s}, &out)
	return out, err
}

// RenewSubscription extends a subscription's expiration.
func (c *Client) RenewSubscription(ctx context.Context, id string, expires time.Time) (Subscription, error) {
	body := struct {
		ExpirationDateTime time.Time `json:"expirationDateTime"`
	}{expires.UTC()}
	var out Subscription
	err := c.do(ctx, request{method: http.MethodPatch, path: "/subscriptions/" + url.PathEscape(id), body: body}, &out)
	return out, err
}

// RetargetSubscription points a subscription at new notification URLs
// (after the relay handed out a new routing token) and renews it.
func (c *Client) RetargetSubscription(ctx context.Context, id, notificationURL, lifecycleURL string, expires time.Time) (Subscription, error) {
	body := struct {
		NotificationURL          string    `json:"notificationUrl"`
		LifecycleNotificationURL string    `json:"lifecycleNotificationUrl,omitempty"`
		ExpirationDateTime       time.Time `json:"expirationDateTime"`
	}{notificationURL, lifecycleURL, expires.UTC()}
	var out Subscription
	err := c.do(ctx, request{method: http.MethodPatch, path: "/subscriptions/" + url.PathEscape(id), body: body}, &out)
	return out, err
}

// DeleteSubscription removes a subscription.
func (c *Client) DeleteSubscription(ctx context.Context, id string) error {
	return c.do(ctx, request{method: http.MethodDelete, path: "/subscriptions/" + url.PathEscape(id)}, nil)
}

// Lifecycle events.
const (
	LifecycleReauthorizationRequired = "reauthorizationRequired"
	LifecycleSubscriptionRemoved     = "subscriptionRemoved"
	LifecycleMissed                  = "missed"
)

// ChangeNotification is one entry of a webhook payload (without resource
// data).
type ChangeNotification struct {
	SubscriptionID                 string     `json:"subscriptionId"`
	SubscriptionExpirationDateTime *time.Time `json:"subscriptionExpirationDateTime,omitempty"`
	ClientState                    string     `json:"clientState"`
	ChangeType                     string     `json:"changeType"`
	Resource                       string     `json:"resource"`
	TenantID                       string     `json:"tenantId"`
	LifecycleEvent                 string     `json:"lifecycleEvent,omitempty"`
	ResourceData                   *struct {
		ID string `json:"id"`
	} `json:"resourceData,omitempty"`
}

// ParseNotifications decodes a webhook body ({"value":[...]}).
func ParseNotifications(body []byte) ([]ChangeNotification, error) {
	var p struct {
		Value []ChangeNotification `json:"value"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	return p.Value, nil
}

// chats('19:...')/messages('123') or chats/19:.../messages/123
var (
	reChatMsgQuoted = regexp.MustCompile(`chats\('([^']+)'\)/messages\('([^']+)'\)`)
	reChatMsgPath   = regexp.MustCompile(`chats/([^/]+)/messages/([^/?]+)`)
)

// ChatMessageRef extracts chat and message ID from a notification resource.
func ChatMessageRef(resource string) (chatID, messageID string, ok bool) {
	if m := reChatMsgQuoted.FindStringSubmatch(resource); m != nil {
		return m[1], m[2], true
	}
	if m := reChatMsgPath.FindStringSubmatch(resource); m != nil {
		c, err1 := url.PathUnescape(m[1])
		msg, err2 := url.PathUnescape(m[2])
		if err1 == nil && err2 == nil {
			return c, msg, true
		}
	}
	return "", "", false
}
