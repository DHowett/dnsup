package main

import (
	"flag"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"time"

	"github.com/miekg/dns"
	"gopkg.in/yaml.v2"
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

func (i *IP) Is4() bool {
	return i.ip.To4() != nil
}

func joinDomain(part, fqdn string) string {
	if part == "@" {
		return dns.Fqdn(fqdn)
	}
	return dns.Fqdn(dns.Fqdn(part) + fqdn)
}

type Config struct {
	Server      string            `yaml:"server"`
	Zone        string            `yaml:"zone"`
	Key         string            `yaml:"key"`
	TsigSecrets map[string]string `yaml:"secrets"`
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

	c := &dns.Client{}
	c.SingleInflight = true
	c.TsigSecret = config.TsigSecrets

	insertions := make([]dns.RR, 0, len(config.Hosts))
	for host, ip := range config.Hosts {
		fqdn := joinDomain(host, config.Zone)
		insertions = append(insertions, &dns.ANY{dns.RR_Header{fqdn, dns.TypeANY, dns.ClassANY, 0, 0}})
		if ip.Is4() {
			finalIp := ip.ApplyIPToMask(four)
			logger.Printf("Updating %s to (IPv4) %v", fqdn, finalIp)
			insertions = append(insertions, &dns.A{dns.RR_Header{fqdn, dns.TypeA, dns.ClassINET, config.Ttl, 0}, finalIp})
		} else {
			finalIp := ip.ApplyIPToMask(sixPrefix)
			logger.Printf("Updating %s to (IPv6) %v", fqdn, finalIp)
			insertions = append(insertions, &dns.AAAA{dns.RR_Header{fqdn, dns.TypeAAAA, dns.ClassINET, config.Ttl, 0}, finalIp})
		}
	}
	insertions = append(insertions, &dns.TXT{dns.RR_Header{dns.Fqdn(config.Zone), dns.TypeTXT, dns.ClassINET, config.Ttl, 0}, []string{time.Now().String()}})
	updateMsg := &dns.Msg{}
	updateMsg.SetUpdate(dns.Fqdn(config.Zone))
	//updateMsg.RemoveName(removals)
	updateMsg.Ns = insertions
	updateMsg.SetTsig(config.Key, dns.HmacMD5, 300, time.Now().UTC().Unix())

	reply, _, err := c.Exchange(updateMsg, config.Server)
	if err != nil {
		logger.Print(reply)
		logger.Fatal("Failed to update DNS sever:", err)
	}

}
