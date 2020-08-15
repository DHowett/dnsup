package main

import (
	"context"
	"flag"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"sync"

	"gopkg.in/yaml.v2"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/dns/mgmt/dns"

	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/Azure/go-autorest/autorest/to"
)

func chooseUnicast(a []net.Addr) []*net.IPNet {
	o := make([]*net.IPNet, 0, len(a))
	for _, addr := range a {
		ipnet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		if !ipnet.IP.IsGlobalUnicast() {
			continue
		}
		o = append(o, ipnet)
	}
	return o
}

func chooseFour(in []*net.IPNet) net.IP {
	for _, inet := range in {
		v4 := inet.IP.To4()
		if v4 != nil {
			return v4
		}
	}
	return nil
}

func chooseSix(in []*net.IPNet) net.IP {
	for _, inet := range in {
		v4 := inet.IP.To4()
		if v4 == nil {
			return inet.IP.Mask(inet.Mask)
		}
	}
	return nil
}

type IP struct {
	ip   net.IP
	mask net.IPMask
}

func (i *IP) UnmarshalYAML(unmarshal func(interface{}) error) error {
	raw := ""
	unmarshal(&raw)
	ip, ipnet, err := net.ParseCIDR(raw)
	if err != nil {
		return err
	}
	i.ip = ip
	i.mask = ipnet.Mask
	return nil
}

func (i *IP) ApplyIPToMask(ip net.IP) net.IP {
	newIp := ip.Mask(i.mask).To16()
	ourIp := i.ip.To16()
	for idx, val := range ourIp {
		newIp[idx] = newIp[idx] | val
	}
	return newIp
}

type AzureConfig struct {
	ClientID       string `yaml:"clientId"`
	ClientSecret   string `yaml:"clientSecret"`
	TenantID       string `yaml:"tenantId"`
	SubscriptionID string `yaml:"subscriptionId"`
	ResourceGroup  string `yaml:"resourceGroup"`
}

type Config struct {
	AzureConfig AzureConfig `yaml:"azure"`
	Zone        string      `yaml:"zone"`
	Hosts       map[string]IP
	Ttl         uint32 `yaml:"ttl"`
}

type DevNull struct{}

func (dn *DevNull) Write(p []byte) (int, error) {
	return len(p), nil
}
func (dn *DevNull) Close() error {
	return nil
}

func (i *IP) Is4() bool {
	return i.ip.To4() != nil
}

/* Azure */
type azureDnsUpdater struct {
	logger        *log.Logger
	dnsClient     dns.RecordSetsClient
	resourceGroup string
	zone          string
	wg            sync.WaitGroup
}

func (a *azureDnsUpdater) SetARecord(host string, ip net.IP) {
	a.wg.Add(1)
	go func() {
		_, err := a.dnsClient.CreateOrUpdate(context.Background(), a.resourceGroup, a.zone, host, "A", dns.RecordSet{
			RecordSetProperties: &dns.RecordSetProperties{
				TTL: to.Int64Ptr(300),
				ARecords: &[]dns.ARecord{
					dns.ARecord{
						Ipv4Address: to.StringPtr(ip.String()),
					},
				},
			},
		}, "", "")
		if err != nil {
			a.logger.Print("Error updating ", host, ": ", err)
		}
		a.wg.Done()
	}()
}
func (a *azureDnsUpdater) SetAAAARecord(host string, ip net.IP) {
	a.wg.Add(1)
	go func() {
		_, err := a.dnsClient.CreateOrUpdate(context.Background(), a.resourceGroup, a.zone, host, "AAAA", dns.RecordSet{
			RecordSetProperties: &dns.RecordSetProperties{
				TTL: to.Int64Ptr(300),
				AaaaRecords: &[]dns.AaaaRecord{
					dns.AaaaRecord{
						Ipv6Address: to.StringPtr(ip.String()),
					},
				},
			},
		}, "", "")
		if err != nil {
			a.logger.Print("Error updating ", host, ": ", err)
		}
		a.wg.Done()
	}()
}
func (a *azureDnsUpdater) Wait() {
	a.wg.Wait()
}
func newAzureDnsUpdater(logger *log.Logger, authorizer autorest.Authorizer, subscription string, resourceGroup string, zone string) *azureDnsUpdater {
	dnsClient := dns.NewRecordSetsClient(subscription)
	dnsClient.Authorizer = authorizer
	return &azureDnsUpdater{
		logger:        logger,
		dnsClient:     dnsClient,
		resourceGroup: resourceGroup,
		zone:          zone,
	}
}

func main() {
	var fourFrom, sixFrom, configFile, logFile string
	flag.StringVar(&fourFrom, "4", "eth0", "pull ipv4 address from")
	flag.StringVar(&sixFrom, "6", "br0", "pull ipv6 address from")
	flag.StringVar(&configFile, "config", "dnsup.yml", "config file (yaml)")
	flag.StringVar(&logFile, "log", "", "log file")
	flag.Parse()

	var logWriter io.WriteCloser = &DevNull{}
	if logFile != "" {
		var err error
		logWriter, err = os.Create(logFile)
		if err != nil {
			panic(err)
		}
	}

	logger := log.New(logWriter, "DNSUp: ", log.LstdFlags)

	config := &Config{}
	configData, err := ioutil.ReadFile(configFile)
	if err != nil {
		logger.Fatal("Failed to read config file:", err)
	}
	err = yaml.Unmarshal(configData, &config)
	if err != nil {
		logger.Fatal("Failed to parse config file:", err)
	}

	var four, sixPrefix net.IP

	ifs, err := net.Interfaces()
	if err != nil {
		logger.Fatal("Failed to get net interfaces:", err)
	}
	for _, iface := range ifs {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		if iface.Name == fourFrom {
			four = chooseFour(chooseUnicast(addrs))
		}
		if iface.Name == sixFrom {
			sixPrefix = chooseSix(chooseUnicast(addrs))
		}
	}

	azureAuthSettings := auth.EnvironmentSettings{
		Values: map[string]string{
			auth.ClientID:     config.AzureConfig.ClientID,
			auth.ClientSecret: config.AzureConfig.ClientSecret,
			auth.TenantID:     config.AzureConfig.TenantID,
			auth.Resource:     azure.PublicCloud.ResourceManagerEndpoint,
		},
		Environment: azure.PublicCloud,
	}
	authorizer, err := azureAuthSettings.GetAuthorizer()
	if err != nil {
		log.Fatal("Failed to authenticate to Azure: ", err)
	}

	updater := newAzureDnsUpdater(logger, authorizer, config.AzureConfig.SubscriptionID, config.AzureConfig.ResourceGroup, config.Zone)

	for host, ip := range config.Hosts {
		if ip.Is4() {
			finalIp := ip.ApplyIPToMask(four)
			logger.Printf("Updating %s to (IPv4) %v", host, finalIp)
			updater.SetARecord(host, finalIp)
		} else {
			finalIp := ip.ApplyIPToMask(sixPrefix)
			logger.Printf("Updating %s to (IPv6) %v", host, finalIp)
			updater.SetAAAARecord(host, finalIp)
		}
	}

	updater.Wait()
	if err != nil {
		//logger.Print(reply)
		logger.Fatal("Failed to update DNS sever:", err)
	}

}
