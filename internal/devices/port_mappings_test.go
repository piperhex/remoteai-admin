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
