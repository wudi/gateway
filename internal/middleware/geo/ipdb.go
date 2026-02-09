package geo

import (
	"fmt"

	"github.com/ipipdotnet/ipdb-go"
)

type ipdbProvider struct {
	db *ipdb.City
}

func newIPDBProvider(path string) (*ipdbProvider, error) {
	db, err := ipdb.NewCity(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open ipdb: %w", err)
	}
	return &ipdbProvider{db: db}, nil
}

func (p *ipdbProvider) Lookup(ip string) (*GeoResult, error) {
	info, err := p.db.FindInfo(ip, "EN")
	if err != nil {
		return nil, fmt.Errorf("ipdb lookup failed: %w", err)
	}

	return &GeoResult{
		CountryCode: info.CountryCode,
		CountryName: info.CountryName,
		City:        info.CityName,
	}, nil
}

func (p *ipdbProvider) Close() error {
	// ipdb-go does not require explicit close
	return nil
}
