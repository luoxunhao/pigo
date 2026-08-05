package acp

import (
	"encoding/json"
	"testing"
)

func TestRequestIDRoundTripNumberAndString(t *testing.T) {
	for _, id := range []RequestID{NumID(42), StrID("abc")} {
		data, err := json.Marshal(id)
		if err != nil {
			t.Fatal(err)
		}
		var got RequestID
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.String() != id.String() {
			t.Fatalf("round trip %v -> %v", id, got)
		}
	}
}

func TestEnvelopeIncomingKinds(t *testing.T) {
	tests := []struct {
		name string
		env  envelope
		want string
	}{
		{"request", envelope{JSONRPC: Version, ID: ptrID(NumID(1)), Method: "initialize"}, "request"},
		{"notification", envelope{JSONRPC: Version, Method: "session/cancel"}, "notification"},
		{"response", envelope{JSONRPC: Version, ID: ptrID(NumID(2)), Result: json.RawMessage(`{"ok":true}`)}, "response"},
		{"error response", envelope{JSONRPC: Version, ID: ptrID(NumID(3)), Error: NewError(CodeMethodNotFound, "nope")}, "response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := tt.env.incoming()
			if err != nil {
				t.Fatal(err)
			}
			switch tt.want {
			case "request":
				if msg.Request == nil || msg.Request.Method != "initialize" {
					t.Fatalf("want request, got %+v", msg)
				}
			case "notification":
				if msg.Notification == nil || msg.Notification.Method != "session/cancel" {
					t.Fatalf("want notification, got %+v", msg)
				}
			case "response":
				if msg.Response == nil {
					t.Fatalf("want response, got %+v", msg)
				}
				if tt.env.Error != nil && msg.Response.Err == nil {
					t.Fatalf("want error response, got %+v", msg.Response)
				}
			}
		})
	}
}

func TestErrorImplementsError(t *testing.T) {
	e := NewError(CodeInvalidParams, "bad params")
	if e.Error() == "" {
		t.Fatal("empty error string")
	}
}

func ptrID(id RequestID) *RequestID { return &id }
