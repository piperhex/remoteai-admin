package devices

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPortMappingValidation(t *testing.T) {
	valid := portMapping{ID: "mapping-1", RemoteHost: "::1", RemotePort: 3000, LocalPort: 13000,
		UpdatedAt: time.Now().UnixMilli()}
	if err := validatePortMappings(mappingDocument{Mappings: []portMapping{valid}}); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*portMapping){
		func(v *portMapping) { v.RemoteHost = "http://127.0.0.1" },
		func(v *portMapping) { v.RemoteHost = "0.0.0.0" },
		func(v *portMapping) { v.RemoteHost = "::" },
		func(v *portMapping) { v.RemotePort = 0 },
		func(v *portMapping) { v.LocalPort = 65536 },
		func(v *portMapping) { v.Protocol = "icmp" },
		func(v *portMapping) { v.LinkProtocol = "file" },
		func(v *portMapping) { v.LocalDomain = "http://app.localhost:8080" },
		func(v *portMapping) { v.LocalDomain = "127.0.0.1" },
		func(v *portMapping) { v.LocalDomain = "host..test" },
		func(v *portMapping) { v.ID = "../mapping" },
		func(v *portMapping) { v.UpdatedAt = time.Now().Add(time.Hour).UnixMilli() },
	} {
		entry := valid
		edit(&entry)
		if validatePortMappings(mappingDocument{Mappings: []portMapping{entry}}) == nil {
			t.Fatal("accepted invalid mapping")
		}
	}
	if validatePortMappings(mappingDocument{Mappings: []portMapping{valid, valid}}) == nil {
		t.Fatal("duplicate IDs")
	}
	if validatePortMappings(mappingDocument{Mappings: nil}) == nil {
		t.Fatal("null mappings")
	}
}

func TestPortMappingDomainPersistsThroughLegacyClientWrites(t *testing.T) {
	entry := portMapping{ID: "mapping", RemoteHost: "127.0.0.1", RemotePort: 3000,
		LocalPort: 13000, LocalDomain: "app.localhost"}
	saved := mappingDocument{Revision: 1, Mappings: []portMapping{entry}}
	legacy := portMappingResponse(saved, false, false)
	if legacy.SupportsLocalDomain || legacy.Mappings[0].LocalDomain != "" || saved.Mappings[0].LocalDomain != "app.localhost" {
		t.Fatal("legacy response must omit the domain without modifying the stored configuration")
	}
	legacy.Mappings[0].RemotePort = 8080
	merged := mergeMappingWrite(saved.Mappings, legacy)
	if merged[0].LocalDomain != "app.localhost" || merged[0].RemotePort != 8080 {
		t.Fatal("a legacy write lost the configured domain")
	}
	latest := portMappingResponse(mappingDocument{Mappings: merged}, true, false)
	if !latest.SupportsLocalDomain || latest.Mappings[0].LocalDomain != "app.localhost" {
		t.Fatal("new clients must see the configured domain")
	}
	latest.Mappings[0].LocalDomain = ""
	if cleared := mergeMappingWrite(saved.Mappings, latest); cleared[0].LocalDomain != "" {
		t.Fatal("new clients must be able to clear the domain")
	}
}

func TestLinkProtocolSurvivesLegacyWriteAndCanBeChangedToHTTP(t *testing.T) {
	doc := mappingDocument{Mappings: []portMapping{{ID: "web", LinkProtocol: "https"}}}
	legacy := portMappingResponse(doc, true, false)
	if legacy.SupportsLinkProtocol || legacy.Mappings[0].LinkProtocol != "" {
		t.Fatal("legacy response exposed the link protocol")
	}
	legacy.Mappings[0].LocalDomain = "edited.localhost"
	saved := mergeMappingWrite(doc.Mappings, legacy)
	current := portMappingResponse(mappingDocument{Mappings: saved}, true, true)
	if !current.SupportsLinkProtocol || current.Mappings[0].LinkProtocol != "https" ||
		current.Mappings[0].LocalDomain != "edited.localhost" {
		t.Fatal("legacy edit lost the protocol or domain")
	}
	current.Mappings[0].LinkProtocol = "http"
	if mergeMappingWrite(saved, current)[0].LinkProtocol != "http" {
		t.Fatal("updated client could not switch back to HTTP")
	}
}

func TestPortMappingProtocolRoundtrip(t *testing.T) {
	for _, protocol := range []string{"", "tcp", "udp", "both"} {
		entry := portMapping{ID: "mapping", RemoteHost: "127.0.0.1", RemotePort: 53, LocalPort: 1053, Protocol: protocol}
		if err := validatePortMappings(mappingDocument{Mappings: []portMapping{entry}}); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		var restored portMapping
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		if restored.Protocol != protocol {
			t.Fatal("protocol was lost")
		}
	}
}

func TestPortMappingMigrationParity(t *testing.T) {
	fresh, err := os.ReadFile("../migrations/015_port_mappings.sql")
	if err != nil {
		t.Fatal(err)
	}
	upgrade, err := os.ReadFile("../../sql/20261015-port-mappings.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(fresh), "\r\n", "\n") != strings.ReplaceAll(string(upgrade), "\r\n", "\n") {
		t.Fatal("fresh and upgrade schema differ")
	}
}
