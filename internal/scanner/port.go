package scanner

import (
	"fmt"
	"net"
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
)

type Scanner struct {
	timeout int
	verbose bool
	version bool
}

func New(timeout int, verbose bool, version bool) *Scanner {
	return &Scanner{
		timeout: timeout,
		verbose: verbose,
		version: version,
	}
}

func (s *Scanner) ScanHost(host, portRange string) {
	fmt.Println(scanStyle.Render(fmt.Sprintf("Scanning %s.....", host)))

	ports := s.parsePorts(portRange)
	openPorts := s.scanPorts(host, ports)

	fmt.Println(scanStyle.Render(fmt.Sprintf("\n Scan Results for %s:", host)))
	fmt.Println(strings.Repeat("-", 50))

	if len(openPorts) == 0 {
		fmt.Println(closedStyle.Render("No open ports found"))
		return
	}

	for _, port := range openPorts {
		service := s.getServiceName(port)
		if s.version {
			version := s.detectVersion(host, port)
			fmt.Printf("%s %d/tcp %s %s\n", openStyle.Render("./"), port, service, version)
		} else {
			fmt.Printf("%s %s/tcp %s\n", openStyle.Render("./"), port, service)
		}
	}
}

func (s *Scanner) ScanRange(ipRange, portRange string) {
	fmt.Println(scanStyle.Render(fmt.Sprintf("Scanning range %s...", ipRange)))
	// TODO: Implement IP range scanning

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
