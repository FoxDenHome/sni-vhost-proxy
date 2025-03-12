package config

import (
	"errors"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

var backendsHttp map[string]*BackendInfo
var backendsHttps map[string]*BackendInfo
var backendsQuic map[string]*BackendInfo
var wildcardsEnabled = false
var Verbose = false

type BackendProtocol int

const (
	PROTO_HTTP BackendProtocol = iota
	PROTO_HTTPS
	PROTO_QUIC
)

const HOST_DEFAULT = "__default__"

type configBackend struct {
	Host            string `yaml:"host"`
	ProxyProtocol   bool   `yaml:"proxy_protocol"`
	Port            int    `yaml:"port"`
	HostPassthrough bool   `yaml:"host_passthrough"`
}

type configHost struct {
	Http     configBackend `yaml:"http"`
	Https    configBackend `yaml:"https"`
	Quic     configBackend `yaml:"quic"`
	Template string        `yaml:"template"`
}

type configBase struct {
	Defaults struct {
		Backends configHost `yaml:"backends"`
	} `yaml:"defaults"`
	Templates map[string]configHost `yaml:"templates"`
	Hosts     map[string]configHost `yaml:"hosts"`
}

type BackendInfo struct {
	Host            string
	ProxyProtocol   bool
	Port            int
	HostPassthrough bool
}

func findBackend(hostname string, backends map[string]*BackendInfo) (*BackendInfo, error) {
	backend, ok := backends[hostname]
	if ok {
		return backend, nil
	}

	if !wildcardsEnabled {
		return backends[HOST_DEFAULT], nil
	}

	hostSplit := strings.Split(hostname, ".")
	if hostSplit[0] == "_" {
		hostSplit = hostSplit[2:]
	} else {
		hostSplit = hostSplit[1:]
	}
	if len(hostSplit) == 0 {
		return backends[HOST_DEFAULT], nil
	}
	return findBackend("_."+strings.Join(hostSplit, "."), backends)
}

func GetBackend(hostname string, protocol BackendProtocol) (*BackendInfo, error) {
	var backends map[string]*BackendInfo
	switch protocol {
	case PROTO_HTTP:
		backends = backendsHttp
	case PROTO_HTTPS:
		backends = backendsHttps
	case PROTO_QUIC:
		backends = backendsQuic
	default:
		return nil, errors.New("invalid protocol")
	}
	return findBackend(hostname, backends)
}

func backendConfigFromConfigHost(cfg *configBackend, port int) *BackendInfo {
	return &BackendInfo{
		Host:            cfg.Host,
		Port:            port,
		ProxyProtocol:   cfg.ProxyProtocol,
		HostPassthrough: cfg.HostPassthrough,
	}
}

func LoadConfig() {
	if os.Getenv("VERBOSE") != "" {
		Verbose = true
	}

	file, err := os.Open(os.Getenv("CONFIG_FILE"))
	if err != nil {
		log.Panicf("Could not open config file: %v", err)
	}
	decoder := yaml.NewDecoder(file)
	var config configBase
	decoder.Decode(&config)

	backendsHttp = make(map[string]*BackendInfo)
	backendsHttps = make(map[string]*BackendInfo)
	backendsQuic = make(map[string]*BackendInfo)

	for host, rawHostConfig := range config.Hosts {
		hostConfig := rawHostConfig
		if rawHostConfig.Template != "" {
			hostConfig = config.Templates[hostConfig.Template]
		}

		if !wildcardsEnabled && strings.HasPrefix(host, "_.") {
			wildcardsEnabled = true
		}

		portHttp := hostConfig.Http.Port
		if portHttp == 0 {
			portHttp = config.Defaults.Backends.Http.Port
		}
		if portHttp > 0 && hostConfig.Http.Host != "" {
			backendsHttp[host] = backendConfigFromConfigHost(&hostConfig.Http, portHttp)
		}

		portHttps := hostConfig.Https.Port
		if portHttps == 0 {
			portHttps = config.Defaults.Backends.Https.Port
		}
		if portHttps > 0 && hostConfig.Https.Host != "" {
			backendsHttps[host] = backendConfigFromConfigHost(&hostConfig.Https, portHttps)
		}

		portQuic := hostConfig.Quic.Port
		if portQuic == 0 {
			portQuic = config.Defaults.Backends.Quic.Port
		}
		if portQuic > 0 && hostConfig.Quic.Host != "" {
			backendsQuic[host] = backendConfigFromConfigHost(&hostConfig.Quic, portQuic)
		}
	}

	log.Printf("Loaded config with %d HTTP host(s), %d HTTPS host(s), %d QUIC host(s), wildard matching %v, verbose %v", len(backendsHttp), len(backendsHttps), len(backendsQuic), wildcardsEnabled, Verbose)
}
