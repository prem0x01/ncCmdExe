package scanner

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	scanStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFD700")).
			Bold(true)

	openStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00FF00"))

	closedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6B6B"))

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#87CEEB"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF4444"))

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFA500"))
)


type ScanType int


const (
	TCPScan ScanType = iota
	UDPScan
	SYNScan // needs root privilege
	CoonectScan
)


type Scanner struct {
	timeout time.Duration
	verbose bool
	version bool
	maxWorkers int
	scanType ScanType
	userAgent string
	skipHostDomain bool
	outputFormat string
	rateLimit time.Duration
	retries int
	proxyURL string
}

type ScannerConfig struct {
	Timeout time.Duration
	Verbose bool
	Version bool
	MaxWorkers int
	ScanType ScanType
	UserAgent string
	SKipHostDomain bool
	OutputFormat string
	RateLimit time.Duration
	Retries int
	ProxyURL string
}

func New(config ScannerConfig) *Scanner {
	if config.Timeout == 0{
		config.Timeout = 3 * time.Second
	}
	if config.MaxWorkers == 0{
		config.MaxWorkers = 100
	}
	if config.UserAgent == ""{
		config.UserAgent = "Net-CMD-EXE/1.0"
	}
	if config.Retries == 0 {
		config.Retries = 1
	}

	return &Scanner{
		timeout: config.Timeout,
		verbose: config.Verbose,
		version: config.Version,
		maxWorkers: config.MaxWorkers,
		scanType: config.ScanType,
		userAgent: config.UserAgent,
		skipHostDomain: config.SKipHostDomain,
		outputFormat: config.OutputFormat,
		rateLimit: config.RateLimit,
		retries: config.Retries,
		proxyURL: config.ProxyURL,
	}
}


type ScanResult struct {
	Host string `json:"host"`
	Port int `json:"port"`
	Protocol string `json:"protocol"`
	Service string `json:"service"`
	Version string `json:"version,omitempty"`
	Banner string `json:"banner,omitempty"`
	Open bool `json:"open"`
	Filtered bool `json:"filtered,omitempty"`
	State string `json:"state"`
	ResponseTime time.Duration `json:"response_time"`
	SSL *SSLInfo `json:"ssl,omitempty"`
	HTTP *HTTPInfo `json:"http,omitempty"`
	Vulnerabilities []string `json:"vulnerabilities,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Timestamp time.Time `json:"timestamp"`

}

type SSLInfo struct {
	Version string `json:"version"`
	Cipher string `json:"cipher"`
	Issuer string `json:"issuer"`
	Subject string `json:"subject"`
	NotBefore time.Time `json:"not_before"`
	NotAfter time.Time `json:"not_after"`
	Fingerprint string `json:"fingerprint"`
}

type HTTPInfo struct {
	StatusCode int `json:"status_code"`
	Server string `json:"server"`
	Title string `json:"title"`
	Headers map[string]string `json:"headers"`
	Redirects []string `json:"redirects,omitempty"`
}

type HostScanResult struct {
	Host         string       `json:"host"`
	IsAlive      bool         `json:"is_alive"`
	OpenPorts    []ScanResult `json:"open_ports"`
	ClosedPorts  []ScanResult `json:"closed_ports,omitempty"`
	FilteredPorts []ScanResult `json:"filtered_ports,omitempty"`
	ScanTime     time.Duration `json:"scan_time"`
	Timestamp    time.Time    `json:"timestamp"`
	OS          *OSInfo      `json:"os,omitempty"`
}


type OSInfo struct {
	Name       string  `json:"name"`
	Version    string  `json:"version"`
	Confidence float64 `json:"confidence"`
	Details    string  `json:"details"`
}


func (s *Scanner) ScanHost(host, portRange string) (*HostScanResult, error) {
	start := time.Now()

	if s.verbose {
		fmt.Println(scanStyle.Render(fmt.Sprintf("Starting scan of  %s.....", host)))
	}

	if !s.skipHostDomain && !s.isHostAlive(host) {
		if s.verbose {
			fmt.Println(warningStyle.Render(fmt.Sprintf("Host %s is down", host)))
		}
		return &HostScanResult{
			Host: host,
			IsAlive: false,
			Timestamp: time.Now(),
			ScanTime: time.Since(start),
		}, nil
	}
	ports, err := s.parsePorts(portRange)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse port range: %w", err)
	}


	resilts := s.scanPorts(host, ports)

	hostResult := &HostScanResult{
		Host: host,
		IsAlive: true,
		Timestamp: time.Now(),
		ScanTime: time.Since(start),
	}

	for _, result := range resilts {
		if result.Opne {
			hostResult.OpenPorts = append(hostResult.OpenPorts, result)
		} else if result.Filtered {
			hostResult.FilteredPorts = append(hostResult.FilteredPorts, result)
		} else {
			hostResult.ClosedPorts = append(hostResult.ClosedPorts, result)
		}
	}

	sort.Slice(hostResult.OpenPorts, func(i, j int) bool {
		resilts hoshostResult.OpenPorts[i].Port < hosthostResult.OpenPorts[j].Port
	})

	if len(hostResult.OpenPorts) > 0 {
		hostResult.OS = s.detectOS(host, hostResult.OpenPorts)
	}

	s.displayResults(hostResult)
	return hostResult, nil
}



func (s *Scanner) ScanRange(ipRange, portRange string) ([]*HostScanResult, error){
	fmt.Println(scanStyle.Render(fmt.Sprintf("Scanning range %s...", ipRange)))

	ips, err := parseIPRange(ipRange)
	if err != nil {
		return nil, err
	}

	var results []*HostScanResult
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, s.maxWorkers)

	for _, ip := range ips {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res, err := s.ScanHost(host, portRange)
			if err == nil {
				mu.Lock()
				results = append(results, res)
				mu.Unlock()
			} else if s.verbose {
                fmt.Println(errorStyle.Render(fmt.Sprintf("Error scannig host %s: %v", host, err)))
            }
		}(ip)
	}

	wg.Wait()
	return results, nil


}

func (s *Scanner) parsePorts(portRange string) []int {
	var ports []int

	if strings.Contains(portRange, "-") {
		parts := strings.Split(portRange, "-")
		if len(parts) == 2 {
			start, _ := strconv.Atoi(parts[0])
			end, _ := strconv.Atoi(parts[1])
			for i := start; i <= end; i++ {
				ports = append(ports, i)
			}
		}
	} else if strings.Contains(portRange, ",") {
		parts := strings.Split(portRange, ",")
		for _, part := range parts {
			if port, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
				ports = append(ports, port)
			}
		}
	} else {
		if port, err := strconv.Atoi(portRange); err == nil {
			ports = append(ports, port)
		}
	}

	return ports
}

func (s *Scanner) scanPorts(host string, ports []int) []int {
	var openPorts []int
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, 100)

	for _, port := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if s.isPortOpen(host, p) {
				mu.Lock()
				openPorts = append(openPorts, p)
				mu.Unlock()

				if s.verbose {
					fmt.Printf("%s %d/tcp\n", openStyle.Render("✓"), p)
				}
			} else if s.verbose {
				fmt.Printf("%s %d/tcp\n", closedStyle.Render("✗"), p)
			}
		}(port)
	}

	wg.Wait()
	return openPorts
}

func (s *Scanner) isPortOpen(host string, port int) bool {
	timeout := time.Duration(s.timeout) * time.Second
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (s *Scanner) getServiceName(port int) string {
	services := map[int]string{
		21:   "ftp",
		22:   "ssh",
		23:   "telnet",
		25:   "smtp",
		53:   "dns",
		80:   "http",
		110:  "pop3",
		135:  "msrpc",
		139:  "netbios-ssn",
		143:  "imap",
		443:  "https",
		993:  "imaps",
		995:  "pop3s",
		1433: "ms-sql-s",
		1521: "oracle",
		3306: "mysql",
		3389: "ms-wbt-server",
		5432: "postgresql",
		5900: "vnc",
		6379: "redis",
		8080: "http-proxy",
		9200: "elasticsearch",
	}

	if service, exists := services[port]; exists {
		return service
	}
	return "unknown"
}

type ScanResult struct {
	Port    int
	Service string
	Version string
	Open    bool
}

func (s *Scanner) ScanHostWithResults(host, portRange string) []ScanResult {
	ports := s.parsePorts(portRange)
	var results []ScanResult
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, 50)

	for _, port := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if s.isPortOpen(host, p) {
				service := s.getServiceName(p)
				version := ""
				if s.version {
					version = s.detectVersion(host, p)
				}

				mu.Lock()
				results = append(results, ScanResult{
					Port:    p,
					Service: service,
					Version: version,
					Open:    true,
				})
				mu.Unlock()
			}
		}(port)
	}

	wg.Wait()
	return results
}

func (s *Scanner) detectVersion(host string, port int) string {
	timeout := time.Duration(s.timeout) * time.Second
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), timeout)
	if err != nil {
		return ""
	}

	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		return ""
	}

	banner := strings.TrimSpace(string(buffer[:n]))
	if len(banner) > 50 {
		banner = banner[:50] + "..."
	}

	return fmt.Sprintf("(%s)", banner)
}

func parseIPRange(ipRange string) ([]string, error) {
	parts := strings.Split(ipRange, "-")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid IP range: expected format 'startIP-endIP'")
	}

	startIP := net.ParseIP(parts[0]).To4()
	endIP := net.ParseIP(parts[1]).To4()
	if startIP == nil || endIP == nil {
		return nil, fmt.Errorf("invalid IP address in range")
	}

	var ips []string
	for ip := startIP; !ip.Equal(endIP); ip = nextIP(ip) {
		ips = append(ips, ip.String())
	}
	ips = append(ips, endIP.String())
	return ips, nil
}

func nextIP(ip net.IP) net.IP {
	ip = ip.To4()
	result := make(net.IP, len(ip))
	copy(result, ip)
	for i := len(result) - 1; i >= 0; i-- {
		result[i]++
		if result[i] != 0 {
			break
		}
	}
	return result
}

