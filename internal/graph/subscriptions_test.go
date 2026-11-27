package graph

import (
	"context"
	"testing"
	"time"
)

func TestSubscriptionCalls(t *testing.T) {
	c, cap := captureServer(t, 201, `{"id":"sub1","expirationDateTime":"2026-01-01T13:00:00Z","resource":"/users/u/chats/getAllMessages","changeType":"created"}`)
	exp := time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC)
	s, err := c.CreateSubscription(context.Background(), Subscription{
		Resource: "/users/u/chats/getAllMessages", ChangeType: "created,updated,deleted",
		NotificationURL: "https://relay/hook?r=t", LifecycleNotificationURL: "https://relay/lifecycle?r=t",
		ClientState: "secret", ExpirationDateTime: exp,
	})
	if err != nil || s.ID != "sub1" {
		t.Fatalf("%+v %v", s, err)
	}
	want := normJSON(t, `{"resource":"/users/u/chats/getAllMessages","changeType":"created,updated,deleted",
		"notificationUrl":"https://relay/hook?r=t","lifecycleNotificationUrl":"https://relay/lifecycle?r=t",
		"clientState":"secret","expirationDateTime":"2026-01-01T13:00:00Z","includeResourceData":false}`)
	if cap.method != "POST" || cap.path != "/v1.0/subscriptions" || asJSON(cap.body) != want {
		t.Fatalf("%s %s\n got %s\nwant %s", cap.method, cap.path, asJSON(cap.body), want)
	}

	_, _ = c.RenewSubscription(context.Background(), "sub1", exp)
	if cap.method != "PATCH" || cap.path != "/v1.0/subscriptions/sub1" || asJSON(cap.body) != `{"expirationDateTime":"2026-01-01T13:00:00Z"}` {
		t.Fatalf("renew %s %s %s", cap.method, cap.path, asJSON(cap.body))
	}
	_, _ = c.RetargetSubscription(context.Background(), "sub1", "https://relay/hook?r=n", "https://relay/lifecycle?r=n", exp)
	if cap.body["notificationUrl"] != "https://relay/hook?r=n" || cap.body["lifecycleNotificationUrl"] != "https://relay/lifecycle?r=n" {
		t.Fatalf("retarget %s", asJSON(cap.body))
	}
	_ = c.DeleteSubscription(context.Background(), "sub1")
	if cap.method != "DELETE" || cap.path != "/v1.0/subscriptions/sub1" {
		t.Fatalf("delete %s %s", cap.method, cap.path)
	}
}

func TestParseNotificationsAndRef(t *testing.T) {
	body := []byte(`{"value":[
	  {"subscriptionId":"s","clientState":"cs","changeType":"created","tenantId":"t",
	   "resource":"chats('19:8b0@unq.gbl.spaces')/messages('1612289765949')","resourceData":{"id":"1612289765949"}},
	  {"subscriptionId":"s","clientState":"cs","lifecycleEvent":"missed","resource":"","changeType":""}]}`)
	ns, err := ParseNotifications(body)
	if err != nil || len(ns) != 2 || ns[1].LifecycleEvent != LifecycleMissed {
		t.Fatalf("%+v %v", ns, err)
	}
	chat, msg, ok := ChatMessageRef(ns[0].Resource)
	if !ok || chat != "19:8b0@unq.gbl.spaces" || msg != "1612289765949" {
		t.Fatalf("%q %q %v", chat, msg, ok)
	}
	chat, msg, ok = ChatMessageRef("chats/19%3Aabc%40thread.v2/messages/42")
	if !ok || chat != "19:abc@thread.v2" || msg != "42" {
		t.Fatalf("path form: %q %q %v", chat, msg, ok)
	}
	if _, _, ok := ChatMessageRef("users/x"); ok {
		t.Fatal("must not match")
	}
	if _, err := ParseNotifications([]byte("{")); err == nil {
		t.Fatal("want error")
	}
}
