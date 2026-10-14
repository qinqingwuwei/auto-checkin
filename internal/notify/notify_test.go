package notify

import (
	"strings"
	"testing"
)

func TestFrozenDingtalkVector(t *testing.T) {
	const secret = "SECtest0123456789abcdef"
	const timestamp = "1700000000000"
	const want = "%2FlfnxUUu80FwVTsIlv2F1cMGBb%2BPxEKAwYh%2FbsRxXcc%3D"
	if got := Sign(secret, timestamp); got != want {
		t.Fatalf("signature %s", got)
	}
	signed := SignedURL("https://example.com/send?access_token=tok", secret, timestamp)
	if !strings.HasSuffix(signed, "&timestamp="+timestamp+"&sign="+want) {
		t.Fatal(signed)
	}
	if SignedURL("https://example.com/send", "", timestamp) != "https://example.com/send" {
		t.Fatal("unsigned URL changed")
	}
}
