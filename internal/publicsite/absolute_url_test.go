package publicsite

import "testing"

func TestAbsoluteURLKeepsCDNAddresses(t *testing.T) {
	if got := absoluteURL("/media/k7f3ab-1024.webp?v=1"); got != SiteOrigin+"/media/k7f3ab-1024.webp?v=1" {
		t.Fatalf("site path = %q", got)
	}
	if got := absoluteURL("https://media.example.com/k7f3ab/v1/1024.webp"); got != "https://media.example.com/k7f3ab/v1/1024.webp" {
		t.Fatalf("cdn url = %q", got)
	}
}
