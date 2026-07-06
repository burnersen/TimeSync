//go:build windows

// ntp-sync keeps the Windows system clock in sync with an NTP server.
// It can synchronize once, run continuously in a console window, or be
// installed as a native Windows service.
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const version = "1.0.0"

const (
	serviceName        = "ntp-sync"
	serviceDisplayName = "NTP Time Synchronizer"
	serviceDescription = "Keeps the Windows system clock in sync with an NTP server."

	ntpServer     = "pool.ntp.org"
	ntpPort       = "123"
	ntpPacketSize = 48
	ntpModeServer = 4
	ntpTimeout    = 5 * time.Second

	// Seconds between the NTP epoch (1900-01-01) and the Unix epoch (1970-01-01).
	ntpEpochOffset = 2208988800

	// Sync interval used when the service is started without one.
	defaultIntervalMinutes = 30

	// Right after boot the network or DNS is often not ready yet, so the
	// first synchronization is retried a few times before falling back to
	// the regular interval.
	startupRetryDelay    = 30 * time.Second
	startupRetryAttempts = 10

	// Server times outside this range are rejected as implausible so that
	// a broken or malicious response can never reset the clock to 1900.
	minPlausibleYear = 2020
	maxPlausibleYear = 2100

	logFileName     = "ntp_sync.log"
	logMaxSizeBytes = 1 << 20 // rotate the log once it exceeds 1 MiB
)

func main() {
	serviceFlag := flag.Bool("service", false, "internal: run as a Windows service (set by the service manager)")
	intervalFlag := flag.Int("interval", 0, "synchronize every N minutes in this console window")
	installFlag := flag.Int("install", 0, "install as a Windows service that synchronizes every N minutes")
	uninstallFlag := flag.Bool("uninstall", false, "remove the Windows service")
	versionFlag := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("%s %s\n", serviceName, version)
		return
	}

	isService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatalf("could not determine whether running as a service: %v", err)
	}
	if isService || *serviceFlag {
		setupServiceLogging()
		runAsService(*intervalFlag)
		return
	}

	// From here on we are in interactive console mode.
	log.SetOutput(os.Stdout)

	if len(os.Args) == 1 {
		printUsage()
	}

	if *installFlag < 0 || *intervalFlag < 0 {
		fmt.Println("[ERROR] The interval must be at least 1 minute.")
		os.Exit(1)
	}

	if !isAdmin() {
		fmt.Println("[ERROR] This action requires administrator privileges.")
		fmt.Println("Please start the console as administrator and try again.")
		os.Exit(1)
	}

	switch {
	case *uninstallFlag:
		fmt.Println("[...] Removing the Windows service...")
		if err := uninstallService(); err != nil {
			fmt.Printf("[ERROR] Uninstall failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[OK] Service removed.")

	case *installFlag > 0:
		fmt.Printf("[...] Installing the service with a %d minute interval...\n", *installFlag)
		if err := installService(*installFlag); err != nil {
			fmt.Printf("[ERROR] Installation failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[OK] Service installed with automatic start.")
		fmt.Printf("Start it now with: sc start %s (or simply reboot)\n", serviceName)

	case *intervalFlag > 0:
		fmt.Printf("[*] Synchronizing every %d minutes, press Ctrl+C to stop...\n", *intervalFlag)
		syncOnceVerbose()
		ticker := time.NewTicker(time.Duration(*intervalFlag) * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			syncOnceVerbose()
		}

	default:
		syncOnceVerbose()
	}
}

// syncOnceVerbose performs one synchronization and reports the result on
// the console.
func syncOnceVerbose() {
	fmt.Printf("[%s] Querying NTP time from %s... ", time.Now().Format("15:04:05"), ntpServer)
	offset, err := fetchNTPOffset(ntpServer)
	if err != nil {
		fmt.Printf("FAILED: %v\n", err)
		return
	}
	fmt.Println("OK.")
	fmt.Printf("    -> measured clock offset: %v\n", offset.Round(time.Millisecond))

	if err := setSystemClock(time.Now().Add(offset)); err != nil {
		fmt.Printf("    [ERROR] Could not set the system clock: %v\n", err)
		return
	}
	fmt.Printf("    [OK] System clock synchronized: %s\n", time.Now().Format("2006-01-02 15:04:05"))
}

// syncSystemClock measures the clock offset against the NTP server and
// adjusts the Windows system clock accordingly. It returns the applied
// offset.
func syncSystemClock() (time.Duration, error) {
	offset, err := fetchNTPOffset(ntpServer)
	if err != nil {
		return 0, err
	}
	if err := setSystemClock(time.Now().Add(offset)); err != nil {
		return 0, fmt.Errorf("could not set the system clock: %w", err)
	}
	return offset, nil
}

// fetchNTPOffset measures the offset between the local clock and the NTP
// server using the four-timestamp exchange from RFC 5905. A positive
// result means the local clock is behind the server.
func fetchNTPOffset(server string) (time.Duration, error) {
	conn, err := net.Dial("udp", net.JoinHostPort(server, ntpPort))
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(ntpTimeout)); err != nil {
		return 0, err
	}

	request := make([]byte, ntpPacketSize)
	request[0] = 0x1B // LI 0, version 3, mode 3 (client)

	// The client transmit time doubles as a nonce: the server must echo
	// it in the originate field, which lets us reject spoofed responses.
	clientSend := time.Now()
	putNTPTimestamp(request[40:48], clientSend)

	if _, err := conn.Write(request); err != nil {
		return 0, err
	}

	response := make([]byte, ntpPacketSize)
	n, err := conn.Read(response)
	clientReceive := time.Now()
	if err != nil {
		return 0, err
	}
	if err := validateNTPResponse(response, n, request[40:48]); err != nil {
		return 0, err
	}

	serverReceive := ntpTimestampToTime(response[32:40])
	serverSend := ntpTimestampToTime(response[40:48])

	// Clock offset per RFC 5905: ((T2 - T1) + (T3 - T4)) / 2.
	offset := (serverReceive.Sub(clientSend) + serverSend.Sub(clientReceive)) / 2
	return offset, nil
}

// validateNTPResponse rejects malformed, unsynchronized, or spoofed
// server responses before their timestamps are trusted.
func validateNTPResponse(response []byte, n int, expectedOriginate []byte) error {
	if n < ntpPacketSize {
		return fmt.Errorf("short response (%d of %d bytes)", n, ntpPacketSize)
	}
	if leap := response[0] >> 6; leap == 3 {
		return errors.New("server clock is not synchronized (leap indicator 3)")
	}
	if mode := response[0] & 0x07; mode != ntpModeServer {
		return fmt.Errorf("unexpected packet mode %d (want %d)", mode, ntpModeServer)
	}
	if stratum := response[1]; stratum == 0 || stratum > 15 {
		return fmt.Errorf("invalid stratum %d", stratum)
	}
	if !bytes.Equal(response[24:32], expectedOriginate) {
		return errors.New("originate timestamp does not match the request (possible spoofing)")
	}
	if isZeroTimestamp(response[32:40]) || isZeroTimestamp(response[40:48]) {
		return errors.New("server sent an empty timestamp")
	}
	serverTime := ntpTimestampToTime(response[40:48]).UTC()
	if year := serverTime.Year(); year < minPlausibleYear || year > maxPlausibleYear {
		return fmt.Errorf("implausible server time %s", serverTime.Format(time.RFC3339))
	}
	return nil
}

// ntpTimestampToTime converts a 64-bit NTP timestamp (seconds since 1900
// plus a 32-bit fraction) into a time.Time. Timestamps with the high bit
// unset are interpreted as NTP era 1 (after February 2036).
func ntpTimestampToTime(b []byte) time.Time {
	secs := int64(binary.BigEndian.Uint32(b[0:4]))
	frac := int64(binary.BigEndian.Uint32(b[4:8]))
	if secs&(1<<31) == 0 {
		secs += 1 << 32
	}
	return time.Unix(secs-ntpEpochOffset, (frac*1e9)>>32)
}

// putNTPTimestamp writes t into b as a 64-bit NTP timestamp.
func putNTPTimestamp(b []byte, t time.Time) {
	binary.BigEndian.PutUint32(b[0:4], uint32(t.Unix()+ntpEpochOffset))
	binary.BigEndian.PutUint32(b[4:8], uint32((int64(t.Nanosecond())<<32)/1e9))
}

func isZeroTimestamp(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// systemTime mirrors the Windows SYSTEMTIME structure expected by the
// kernel32 SetSystemTime call.
type systemTime struct {
	wYear         uint16
	wMonth        uint16
	wDayOfWeek    uint16
	wDay          uint16
	wHour         uint16
	wMinute       uint16
	wSecond       uint16
	wMilliseconds uint16
}

var (
	kernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procSetSystemTime = kernel32.NewProc("SetSystemTime")
)

// setSystemClock sets the Windows system clock, which expects UTC.
func setSystemClock(t time.Time) error {
	utc := t.UTC()
	st := systemTime{
		wYear:         uint16(utc.Year()),
		wMonth:        uint16(utc.Month()),
		wDayOfWeek:    uint16(utc.Weekday()),
		wDay:          uint16(utc.Day()),
		wHour:         uint16(utc.Hour()),
		wMinute:       uint16(utc.Minute()),
		wSecond:       uint16(utc.Second()),
		wMilliseconds: uint16(utc.Nanosecond() / 1e6),
	}

	ret, _, callErr := procSetSystemTime.Call(uintptr(unsafe.Pointer(&st)))
	if ret == 0 {
		return callErr
	}
	return nil
}

// isAdmin reports whether the process runs elevated (as administrator).
func isAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// setupServiceLogging sends log output to a file next to the executable,
// because a service has no console. Logging stays on stderr if the file
// cannot be opened.
func setupServiceLogging() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	logPath := filepath.Join(filepath.Dir(exePath), logFileName)
	rotateLogIfLarge(logPath)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(file)
}

// rotateLogIfLarge renames an oversized log file to *.old so the log can
// never grow without bound. Any previous .old file is replaced.
func rotateLogIfLarge(logPath string) {
	info, err := os.Stat(logPath)
	if err != nil || info.Size() < logMaxSizeBytes {
		return
	}
	oldPath := logPath + ".old"
	_ = os.Remove(oldPath) // Windows cannot rename over an existing file
	_ = os.Rename(logPath, oldPath)
}

// --- Windows service implementation ---

type timeSyncService struct {
	interval time.Duration
}

func (s *timeSyncService) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	status <- svc.Status{State: svc.StartPending}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	log.Printf("[INFO] Service started. Sync interval: %v", s.interval)

	stop := make(chan struct{})
	defer close(stop)
	go runInitialSync(stop)

	for {
		select {
		case <-ticker.C:
			if offset, err := syncSystemClock(); err != nil {
				log.Printf("[ERROR] Synchronization failed: %v", err)
			} else {
				log.Printf("[NTP] Synchronization successful, clock offset was %v", offset.Round(time.Millisecond))
			}
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				log.Println("[INFO] Service stopping.")
				return false, 0
			}
		}
	}
}

// runInitialSync performs the first synchronization after service start.
// It retries a few times because DNS is often not ready right after boot.
func runInitialSync(stop <-chan struct{}) {
	for attempt := 1; attempt <= startupRetryAttempts; attempt++ {
		offset, err := syncSystemClock()
		if err == nil {
			log.Printf("[NTP] Initial synchronization successful, clock offset was %v", offset.Round(time.Millisecond))
			return
		}
		log.Printf("[ERROR] Initial synchronization failed (attempt %d/%d): %v", attempt, startupRetryAttempts, err)
		select {
		case <-time.After(startupRetryDelay):
		case <-stop:
			return
		}
	}
	log.Printf("[ERROR] Initial synchronization gave up after %d attempts, continuing at the regular interval.", startupRetryAttempts)
}

func runAsService(intervalMinutes int) {
	if intervalMinutes <= 0 {
		intervalMinutes = defaultIntervalMinutes
	}
	service := &timeSyncService{interval: time.Duration(intervalMinutes) * time.Minute}
	if err := svc.Run(serviceName, service); err != nil {
		log.Fatalf("service failed: %v", err)
	}
}

func installService(intervalMinutes int) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	existing, err := m.OpenService(serviceName)
	if err == nil {
		existing.Close()
		return errors.New("the service is already installed")
	}

	config := mgr.Config{
		DisplayName: serviceDisplayName,
		Description: serviceDescription,
		StartType:   mgr.StartAutomatic,
	}
	// The extra arguments become part of the service command line, so the
	// service knows its sync interval on every start.
	s, err := m.CreateService(serviceName, exePath, config,
		"-service", "-interval", strconv.Itoa(intervalMinutes))
	if err != nil {
		return err
	}
	s.Close()
	return nil
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return errors.New("service not found (is it already uninstalled?)")
	}
	defer s.Close()

	stopService(s)
	return s.Delete()
}

// stopService asks the service to stop and waits briefly until it reports
// the stopped state, so deletion does not race a still-running process.
func stopService(s *mgr.Service) {
	status, err := s.Control(svc.Stop)
	if err != nil {
		return // most likely not running
	}
	deadline := time.Now().Add(10 * time.Second)
	for status.State != svc.Stopped && time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		status, err = s.Query()
		if err != nil {
			return
		}
	}
}

// printUsage shows the start dashboard when no arguments are given.
func printUsage() {
	fmt.Println("==================================================================")
	fmt.Printf("        %s %s — %s\n", serviceName, version, serviceDisplayName)
	fmt.Println("==================================================================")
	fmt.Printf(" Current local system time : %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf(" Administrator privileges  : %t\n", isAdmin())
	fmt.Printf(" NTP reference server      : %s\n", ntpServer)
	fmt.Println("------------------------------------------------------------------")
	fmt.Println(" USAGE (administrator privileges required):")
	fmt.Println()
	fmt.Println(" 1. Synchronize once, right now:")
	fmt.Println("    > ntp-sync.exe")
	fmt.Println()
	fmt.Println(" 2. Keep synchronizing every N minutes in this console window:")
	fmt.Println("    > ntp-sync.exe -interval 30")
	fmt.Println()
	fmt.Println(" 3. Install as a Windows service (starts with every boot):")
	fmt.Println("    > ntp-sync.exe -install 60")
	fmt.Println()
	fmt.Println(" 4. Remove the Windows service:")
	fmt.Println("    > ntp-sync.exe -uninstall")
	fmt.Println()
	fmt.Println(" 5. Show the version:")
	fmt.Println("    > ntp-sync.exe -version")
	fmt.Println("==================================================================")
}
