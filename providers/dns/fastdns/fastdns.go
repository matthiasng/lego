// Package fastdns implements a DNS provider for solving the DNS-01 challenge using FastDNS.
package fastdns

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	configdns "github.com/akamai/AkamaiOPEN-edgegrid-golang/configdns-v1"
	"github.com/akamai/AkamaiOPEN-edgegrid-golang/edgegrid"
	"github.com/go-acme/lego/v3/challenge/dns01"
	"github.com/go-acme/lego/v3/platform/config/env"
)

// Environment variables names.
const (
	envNamespace = "AKAMAI_"

	EnvHost         = envNamespace + "HOST"
	EnvClientToken  = envNamespace + "CLIENT_TOKEN"
	EnvClientSecret = envNamespace + "CLIENT_SECRET"
	EnvAccessToken  = envNamespace + "ACCESS_TOKEN"

	EnvTTL                = envNamespace + "TTL"
	EnvPropagationTimeout = envNamespace + "PROPAGATION_TIMEOUT"
	EnvPollingInterval    = envNamespace + "POLLING_INTERVAL"
)

// Config is used to configure the creation of the DNSProvider.
type Config struct {
	edgegrid.Config
	PropagationTimeout time.Duration
	PollingInterval    time.Duration
	TTL                int
}

// NewDefaultConfig returns a default configuration for the DNSProvider.
func NewDefaultConfig() *Config {
	return &Config{
		TTL:                env.GetOrDefaultInt(EnvTTL, dns01.DefaultTTL),
		PropagationTimeout: env.GetOrDefaultSecond(EnvPropagationTimeout, dns01.DefaultPropagationTimeout),
		PollingInterval:    env.GetOrDefaultSecond(EnvPollingInterval, dns01.DefaultPollingInterval),
	}
}

// DNSProvider implements the challenge.Provider interface.
type DNSProvider struct {
	config *Config
}

// NewDNSProvider uses the supplied environment variables to return a DNSProvider instance:
// AKAMAI_HOST, AKAMAI_CLIENT_TOKEN, AKAMAI_CLIENT_SECRET, AKAMAI_ACCESS_TOKEN.
func NewDNSProvider() (*DNSProvider, error) {
	values, err := env.Get(EnvHost, EnvClientToken, EnvClientSecret, EnvAccessToken)
	if err != nil {
		return nil, fmt.Errorf("fastdns: %w", err)
	}

	config := NewDefaultConfig()
	config.Config = edgegrid.Config{
		Host:         values[EnvHost],
		ClientToken:  values[EnvClientToken],
		ClientSecret: values[EnvClientSecret],
		AccessToken:  values[EnvAccessToken],
		MaxBody:      131072,
	}

	return NewDNSProviderConfig(config)
}

// NewDNSProviderConfig return a DNSProvider instance configured for FastDNS.
func NewDNSProviderConfig(config *Config) (*DNSProvider, error) {
	if config == nil {
		return nil, errors.New("fastdns: the configuration of the DNS provider is nil")
	}

	if config.ClientToken == "" || config.ClientSecret == "" || config.AccessToken == "" || config.Host == "" {
		return nil, errors.New("fastdns: credentials are missing")
	}

	return &DNSProvider{config: config}, nil
}

// Present creates a TXT record to fulfill the DNS-01 challenge.
func (d *DNSProvider) Present(domain, token, keyAuth string) error {
	fqdn, value := dns01.GetRecord(domain, keyAuth)
	return d.CreateRecord(domain, token, fqdn, value)
}

// CreateRecord creates a TXT record to fulfill the DNS-01 challenge.
func (d *DNSProvider) CreateRecord(domain, token, fqdn, value string) error {
	zoneName, recordName, err := d.findZoneAndRecordName(fqdn, domain)
	if err != nil {
		return fmt.Errorf("fastdns: %w", err)
	}

	configdns.Init(d.config.Config)

	zone, err := configdns.GetZone(zoneName)
	if err != nil {
		return fmt.Errorf("fastdns: %w", err)
	}

	record := configdns.NewTxtRecord()
	_ = record.SetField("name", recordName)
	_ = record.SetField("ttl", d.config.TTL)
	_ = record.SetField("target", value)
	_ = record.SetField("active", true)

	for _, r := range zone.Zone.Txt {
		if r != nil && reflect.DeepEqual(r.ToMap(), record.ToMap()) {
			return nil
		}
	}

	return d.createRecord(zone, record)
}

// CleanUp removes the TXT record matching the specified parameters.
func (d *DNSProvider) CleanUp(domain, token, keyAuth string) error {
	fqdn, value := dns01.GetRecord(domain, keyAuth)
	return d.DeleteRecord(domain, token, fqdn, value)
}

// DeleteRecord removes the record matching the specified parameters.
func (d *DNSProvider) DeleteRecord(domain, token, fqdn, value string) error {
	zoneName, recordName, err := d.findZoneAndRecordName(fqdn, domain)
	if err != nil {
		return fmt.Errorf("fastdns: %w", err)
	}

	configdns.Init(d.config.Config)

	zone, err := configdns.GetZone(zoneName)
	if err != nil {
		return fmt.Errorf("fastdns: %w", err)
	}

	var removed bool
	for _, r := range zone.Zone.Txt {
		if r != nil && r.Name == recordName {
			if zone.RemoveRecord(r) != nil {
				return fmt.Errorf("fastdns: %w", err)
			}
			removed = true
		}
	}

	if removed {
		return zone.Save()
	}

	return nil
}

// Timeout returns the timeout and interval to use when checking for DNS propagation.
// Adjusting here to cope with spikes in propagation times.
func (d *DNSProvider) Timeout() (timeout, interval time.Duration) {
	return d.config.PropagationTimeout, d.config.PollingInterval
}

func (d *DNSProvider) findZoneAndRecordName(fqdn, domain string) (string, string, error) {
	zone, err := dns01.FindZoneByFqdn(dns01.ToFqdn(domain))
	if err != nil {
		return "", "", err
	}
	zone = dns01.UnFqdn(zone)
	name := dns01.UnFqdn(fqdn)
	name = name[:len(name)-len("."+zone)]

	return zone, name, nil
}

func (d *DNSProvider) createRecord(zone *configdns.Zone, record *configdns.TxtRecord) error {
	err := zone.AddRecord(record)
	if err != nil {
		return err
	}

	return zone.Save()
}
