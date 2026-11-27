package devices

import (
	"context"
	"errors"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/gorm"
)

type portMapping struct {
	ID           string `json:"id"`
	RemoteHost   string `json:"remoteHost"`
	RemotePort   int    `json:"remotePort"`
	LocalPort    int    `json:"localPort"`
	Protocol     string `json:"protocol,omitempty"`
	UpdatedAt    int64  `json:"updatedAt,omitempty"`
	LocalDomain  string `json:"localDomain,omitempty"`
	LinkProtocol string `json:"linkProtocol,omitempty"`
}

type mappingDocument struct {
	OwnerID              string        `gorm:"column:ownerId;primaryKey" json:"-"`
	DeviceID             string        `gorm:"column:deviceId;primaryKey" json:"-"`
	Revision             int64         `json:"revision"`
	Mappings             []portMapping `gorm:"serializer:json" json:"mappings"`
	Accepted             bool          `gorm:"-" json:"accepted"`
	SupportsLocalDomain  bool          `gorm:"-" json:"supportsLocalDomain,omitempty"`
	SupportsLinkProtocol bool          `gorm:"-" json:"supportsLinkProtocol,omitempty"`
}

func (mappingDocument) TableName() string { return "device_port_mappings" }

var mappingID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var mappingDomainLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (s *Service) registerPortMappings(group *gin.RouterGroup) {
	group.GET("/:deviceId/port-mappings", s.portMappings)
	group.PUT("/:deviceId/port-mappings", s.portMappings)
}

func (s *Service) portMappings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	owner, device := platform.User(c).ID, c.Param("deviceId")
	if _, err := s.owned(owner, device); err != nil {
		platform.Respond(c, nil, err)
		return
	}
	var incoming *mappingDocument
	supportsDomain := c.Query("localDomain") == "1"
	supportsLinkProtocol := c.Query("linkProtocol") == "1"
	if c.Request.Method == http.MethodPut {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
		incoming = &mappingDocument{}
		if c.ShouldBindJSON(incoming) != nil || validatePortMappings(*incoming) != nil {
			platform.Fail(c, 400, "Invalid port mapping configuration")
			return
		}
		incoming.SupportsLocalDomain = supportsDomain
		incoming.SupportsLinkProtocol = supportsLinkProtocol
	}
	result, err := s.syncPortMappings(c.Request.Context(), owner, device, incoming)
	platform.Respond(c, portMappingResponse(result, supportsDomain, supportsLinkProtocol), err)
}

func portMappingResponse(result mappingDocument, supportsDomain, supportsLinkProtocol bool) mappingDocument {
	result.SupportsLocalDomain = supportsDomain
	result.SupportsLinkProtocol = supportsLinkProtocol
	if !supportsDomain || !supportsLinkProtocol {
		result.Mappings = slices.Clone(result.Mappings)
		for i := range result.Mappings {
			if !supportsDomain {
				result.Mappings[i].LocalDomain = ""
			}
			if !supportsLinkProtocol {
				result.Mappings[i].LinkProtocol = ""
			}
		}
	}
	return result
}

func validMappingDomain(domain string) bool {
	if domain == "" || domain == "localhost" {
		return true
	}
	labels := strings.Split(domain, ".")
	if len(domain) > 253 || len(labels) < 2 {
		return false
	}
	last := labels[len(labels)-1]
	if len(last) == 0 || last[0] < 'a' || last[0] > 'z' {
		return false
	}
	return !slices.ContainsFunc(labels, func(label string) bool { return !mappingDomainLabel.MatchString(label) })
}

func validatePortMappings(doc mappingDocument) error {
	invalid := errors.New("invalid port mapping configuration")
	if doc.Revision < 0 || len(doc.Mappings) > 8 || doc.Mappings == nil {
		return invalid
	}
	seen := map[string]bool{}
	for _, entry := range doc.Mappings {
		if entry.Protocol != "" && entry.Protocol != "tcp" && entry.Protocol != "udp" && entry.Protocol != "both" {
			return invalid
		}
		if entry.LinkProtocol != "" && entry.LinkProtocol != "http" && entry.LinkProtocol != "https" {
			return invalid
		}
		ip := net.ParseIP(entry.RemoteHost)
		if !mappingID.MatchString(entry.ID) || seen[entry.ID] || ip == nil || ip.IsUnspecified() ||
			!validMappingDomain(entry.LocalDomain) ||
			entry.RemotePort < 1 || entry.RemotePort > 65535 || entry.LocalPort < 1 || entry.LocalPort > 65535 ||
			entry.UpdatedAt < 0 || entry.UpdatedAt > time.Now().Add(5*time.Minute).UnixMilli() {
			return invalid
		}
		seen[entry.ID] = true
	}
	return nil
}

func readPortMappings(db *gorm.DB, owner, device string) (mappingDocument, error) {
	result := mappingDocument{OwnerID: owner, DeviceID: device, Mappings: []portMapping{}}
	err := db.Where(`"ownerId" = ? AND "deviceId" = ?`, owner, device).First(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = nil
	}
	return result, err
}

// Compare-and-swap rejects stale snapshots, including snapshots predating a deletion.
func (s *Service) syncPortMappings(ctx context.Context, owner, device string,
	incoming *mappingDocument) (mappingDocument, error) {
	if incoming == nil {
		return s.cachedPortMappings(ctx, owner, device)
	}
	var result mappingDocument
	err := s.deps.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "port-mappings:"+owner+":"+device).Error; err != nil {
			return err
		}
		var err error
		result, err = readPortMappings(tx, owner, device)
		if err != nil || incoming.Revision != result.Revision {
			return err
		}
		result.Mappings = mergeMappingWrite(result.Mappings, *incoming)
		result.Revision++
		result.Accepted = true
		return tx.Save(&result).Error
	})
	if err == nil && result.Accepted {
		platform.InvalidatePortMappings(s.deps, owner, device)
	}
	return result, err
}

func mergeMappingWrite(saved []portMapping, incoming mappingDocument) []portMapping {
	if incoming.SupportsLocalDomain && incoming.SupportsLinkProtocol {
		return incoming.Mappings
	}
	// Older clients upload complete documents without fields they cannot represent.
	previous := make(map[string]portMapping, len(saved))
	for _, entry := range saved {
		previous[entry.ID] = entry
	}
	mappings := slices.Clone(incoming.Mappings)
	for i := range mappings {
		prior := previous[mappings[i].ID]
		if !incoming.SupportsLocalDomain {
			mappings[i].LocalDomain = prior.LocalDomain
		}
		if !incoming.SupportsLinkProtocol {
			mappings[i].LinkProtocol = prior.LinkProtocol
		}
	}
	return mappings
}
