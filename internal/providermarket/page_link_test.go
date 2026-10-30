package providermarket

import (
	"github.com/codex-switch/admin-go/internal/platform"
	"testing"
)

func TestPageLinkValidationAndPublicationRequirement(t *testing.T) {
	page := PageLink{URL: "https://example.test/dashboard?existing=1#balance", Query: "tag=a&tag=b",
		Headers: map[string]string{"X-Region": "east"}}
	if !validPageLink(&page) {
		t.Fatal("valid page rejected")
	}
	for _, mutate := range []func(*PageLink){
		func(p *PageLink) { p.URL = "javascript:alert(1)" },
		func(p *PageLink) { p.URL = "https://user:secret@example.test" },
		func(p *PageLink) { p.Query = "x=1#fragment" },
		func(p *PageLink) { p.Headers = map[string]string{"X-Region": "east\r\nInjected: yes"} },
		func(p *PageLink) { p.Headers = map[string]string{"X-Region": "east", "x-region": "west"} },
		func(p *PageLink) { p.Headers = map[string]string{"Host": "other.test"} },
	} {
		changed := page
		mutate(&changed)
		if validPageLink(&changed) {
			t.Fatal("invalid page accepted")
		}
	}
	legacy := fixture()
	legacy.PageLink = nil
	if validate(legacy) != nil {
		t.Fatal("legacy entry cannot be read")
	}
	router := testRouter(t, nil, map[string]*platform.Principal{"user": {ID: "owner"}})
	response := call(router, "user", "POST", "/provider-market", saveInput{Draft: legacy, Published: pointer(true)})
	if response.Code != 400 {
		t.Fatal("publication without page accepted", response.Code)
	}
}
