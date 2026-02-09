package geo

import (
	"fmt"
	"net/netip"

	"github.com/oschwald/maxminddb-golang/v2"
)

type mmdbProvider struct {
	db *maxminddb.Reader
}

// mmdbRecord maps the nested MaxMind GeoIP2/GeoLite2 city structure.
type mmdbRecord struct {
	Country struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"country"`
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
}

func newMMDBProvider(path string) (*mmdbProvider, error) {
	db, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open mmdb: %w", err)
	}
	return &mmdbProvider{db: db}, nil
}

func (p *mmdbProvider) Lookup(ip string) (*GeoResult, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil, fmt.Errorf("invalid IP address: %w", err)
	}

	var record mmdbRecord
	if err := p.db.Lookup(addr).Decode(&record); err != nil {
		return nil, fmt.Errorf("mmdb lookup failed: %w", err)
	}

	return &GeoResult{
		CountryCode: record.Country.ISOCode,
		CountryName: record.Country.Names["en"],
		City:        record.City.Names["en"],
	}, nil
}

func (p *mmdbProvider) Close() error {
	return p.db.Close()
}
