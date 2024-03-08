package geodb

import (
	"fmt"
	"net"
	"sync"

	"github.com/oschwald/geoip2-golang"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
)

const (
	cityDatabaseFilepath = "/etc/maxmind/GeoLite2-City.mmdb"
	asnDatabaseFilepath  = "/etc/maxmind/GeoLite2-ASN.mmdb"
)

type GeoDatabase interface {
	City(ip net.IP) (v1.IPGeoInfo, error)
	ASN(ip net.IP) (string, error)
}

type GeoDB struct {
	cityDB *geoip2.Reader
	asnDB  *geoip2.Reader
	mu     sync.Mutex
}

func NewGeoDB() (*GeoDB, error) {
	cdb, err := geoip2.Open(cityDatabaseFilepath)
	if err != nil {
		cdb.Close()
		return &GeoDB{}, err
	}

	adb, err := geoip2.Open(asnDatabaseFilepath)
	if err != nil {
		cdb.Close()
		adb.Close()
		return &GeoDB{}, err
	}

	return &GeoDB{cityDB: cdb, asnDB: adb, mu: sync.Mutex{}}, nil
}

func (g *GeoDB) City(ip net.IP) (v1.IPGeoInfo, error) {
	IPInfo := v1.IPGeoInfo{}
	g.mu.Lock()
	defer g.mu.Unlock()
	geoInfo, err := g.cityDB.City(ip)
	if err != nil {
		return IPInfo, err
	} else if geoInfo != nil {
		IPInfo = v1.IPGeoInfo{
			CountryName: geoInfo.Country.Names["en"],
			CityName:    geoInfo.City.Names["en"],
			ISO:         geoInfo.Country.IsoCode,
		}
	}
	return IPInfo, nil
}

func (g *GeoDB) ASN(ip net.IP) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	asnInfo, err := g.asnDB.ASN(ip)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%v", asnInfo.AutonomousSystemNumber), nil
}

func (g *GeoDB) Close() {
	g.asnDB.Close()
	g.cityDB.Close()
}
