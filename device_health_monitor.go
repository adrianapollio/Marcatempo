package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
)

type SystemDeviceHealthSummary struct {
	MonitorEnabled      bool       `json:"monitor_enabled"`
	AutomaticMode       bool       `json:"automatic_mode"`
	CheckMethod         string     `json:"check_method"`
	IntervalSeconds     int        `json:"interval_seconds"`
	TimeoutSeconds      int        `json:"timeout_seconds"`
	Port                int        `json:"port"`
	IncludeInactive     bool       `json:"include_inactive"`
	OnlineCount         int        `json:"online_count"`
	OfflineCount        int        `json:"offline_count"`
	UnknownCount        int        `json:"unknown_count"`
	LastSweepAt         *time.Time `json:"last_sweep_at,omitempty"`
	LastSweepDurationMs int64      `json:"last_sweep_duration_ms"`
	SnmpTrapEnabled     bool       `json:"snmp_trap_enabled"`
	SnmpTrapTarget      string     `json:"snmp_trap_target"`
}

type SystemDeviceHealthEvent struct {
	Timestamp      time.Time `json:"timestamp"`
	DeviceID       int       `json:"device_id"`
	IP             string    `json:"ip"`
	FromStatus     string    `json:"from_status"`
	ToStatus       string    `json:"to_status"`
	Reason         string    `json:"reason"`
	TrapAttempted  bool      `json:"trap_attempted"`
	TrapSuccessful bool      `json:"trap_successful"`
	TrapError      string    `json:"trap_error,omitempty"`
}

type deviceHealthConfig struct {
	Enabled           bool
	Automatic         bool
	Interval          time.Duration
	Timeout           time.Duration
	Port              int
	IncludeInactive   bool
	EventBuffer       int
	SnmpTrapEnabled   bool
	SnmpTrapTarget    string
	SnmpTrapPort      int
	SnmpTrapCommunity string
	SnmpTrapBaseOID   string
}

type deviceHealthState struct {
	DeviceID            uint32
	IP                  string
	Configured          bool
	Active              bool
	ReachabilityKnown   bool
	Reachable           bool
	LastCheckAt         *time.Time
	LastReachableAt     *time.Time
	LastUnreachableAt   *time.Time
	LastChangeAt        *time.Time
	ConsecutiveFailures int
	LastCheckDurationMs int64
	LastCheckError      string
}

func (s deviceHealthState) status() string {
	if !s.ReachabilityKnown {
		return "unknown"
	}
	if s.Reachable {
		return "online"
	}
	return "offline"
}

type deviceHealthMonitor struct {
	mu                  sync.RWMutex
	cfg                 deviceHealthConfig
	states              map[uint32]deviceHealthState
	events              []SystemDeviceHealthEvent
	lastSweepAt         *time.Time
	lastSweepDurationMs int64
	sweepRunning        bool
	started             bool
}

var globalDeviceHealthMonitor = &deviceHealthMonitor{}

func loadDeviceHealthConfig() deviceHealthConfig {
	intervalSeconds := envInt("ANVIZ_HEALTHCHECK_INTERVAL_SECONDS", 60)
	if intervalSeconds < 5 {
		intervalSeconds = 5
	}

	timeoutSeconds := envInt("ANVIZ_HEALTHCHECK_TIMEOUT_SECONDS", 3)
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}

	port := envInt("ANVIZ_HEALTHCHECK_PORT", 5010)
	if port <= 0 || port > 65535 {
		port = 5010
	}

	eventBuffer := envInt("ANVIZ_HEALTHCHECK_EVENT_BUFFER", 100)
	if eventBuffer < 10 {
		eventBuffer = 10
	}

	snmpPort := envInt("SNMP_TRAP_PORT", 162)
	if snmpPort <= 0 || snmpPort > 65535 {
		snmpPort = 162
	}

	snmpCommunity := strings.TrimSpace(os.Getenv("SNMP_TRAP_COMMUNITY"))
	if snmpCommunity == "" {
		snmpCommunity = "public"
	}

	baseOID := strings.TrimSpace(os.Getenv("SNMP_TRAP_BASE_OID"))
	if baseOID == "" {
		baseOID = ".1.3.6.1.4.1.53864.1"
	}

	return deviceHealthConfig{
		Enabled:           envBool("ANVIZ_HEALTHCHECK_ENABLED", true),
		Automatic:         envBool("ANVIZ_HEALTHCHECK_AUTOMATIC", true),
		Interval:          time.Duration(intervalSeconds) * time.Second,
		Timeout:           time.Duration(timeoutSeconds) * time.Second,
		Port:              port,
		IncludeInactive:   envBool("ANVIZ_HEALTHCHECK_INCLUDE_INACTIVE", true),
		EventBuffer:       eventBuffer,
		SnmpTrapEnabled:   envBool("SNMP_TRAP_ENABLED", false),
		SnmpTrapTarget:    strings.TrimSpace(os.Getenv("SNMP_TRAP_TARGET")),
		SnmpTrapPort:      snmpPort,
		SnmpTrapCommunity: snmpCommunity,
		SnmpTrapBaseOID:   baseOID,
	}
}

func initDeviceHealthMonitor(configured []AnvizDevice, active []AnvizDevice) {
	globalDeviceHealthMonitor.start(configured, active)
}

func (m *deviceHealthMonitor) start(configured []AnvizDevice, active []AnvizDevice) {
	cfg := loadDeviceHealthConfig()

	m.mu.Lock()
	m.cfg = cfg
	m.states = make(map[uint32]deviceHealthState, len(configured))
	m.events = nil
	m.lastSweepAt = nil
	m.lastSweepDurationMs = 0
	m.started = cfg.Enabled

	activeSet := make(map[uint32]struct{}, len(active))
	for _, d := range active {
		activeSet[d.ID] = struct{}{}
	}

	for _, d := range configured {
		_, isActive := activeSet[d.ID]
		m.states[d.ID] = deviceHealthState{
			DeviceID:   d.ID,
			IP:         d.IP,
			Configured: true,
			Active:     isActive,
		}
	}
	m.mu.Unlock()

	if !cfg.Enabled {
		log.Println("ANVIZ health monitor disabilitato da ANVIZ_HEALTHCHECK_ENABLED")
		return
	}

	if cfg.SnmpTrapEnabled && cfg.SnmpTrapTarget == "" {
		log.Println("ANVIZ health monitor: SNMP trap richiesto ma SNMP_TRAP_TARGET mancante, trap disabilitato")
		m.mu.Lock()
		m.cfg.SnmpTrapEnabled = false
		m.mu.Unlock()
	}

	log.Printf("ANVIZ health monitor avviato interval=%s timeout=%s port=%d include_inactive=%v snmp_trap=%v target=%s",
		cfg.Interval, cfg.Timeout, cfg.Port, cfg.IncludeInactive, cfg.SnmpTrapEnabled, cfg.SnmpTrapTarget)

	if cfg.Automatic {
		go m.loop()
	} else {
		log.Println("ANVIZ health monitor in modalita manuale (nessun sweep periodico automatico)")
	}
}

func (m *deviceHealthMonitor) loop() {
	_ = m.runSweep()

	m.mu.RLock()
	interval := m.cfg.Interval
	m.mu.RUnlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		_ = m.runSweep()
	}
}

func (m *deviceHealthMonitor) runSweep() error {
	m.mu.Lock()
	if !m.cfg.Enabled {
		m.mu.Unlock()
		return fmt.Errorf("health monitor disabilitato")
	}
	if m.sweepRunning {
		m.mu.Unlock()
		return fmt.Errorf("health sweep gia in corso")
	}
	m.sweepRunning = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.sweepRunning = false
		m.mu.Unlock()
	}()

	startedAt := time.Now()

	m.mu.RLock()
	cfg := m.cfg
	statesSnapshot := make([]deviceHealthState, 0, len(m.states))
	for _, state := range m.states {
		statesSnapshot = append(statesSnapshot, state)
	}
	m.mu.RUnlock()

	for _, state := range statesSnapshot {
		if !cfg.IncludeInactive && !state.Active {
			continue
		}
		m.checkOne(state.DeviceID)
	}

	durationMs := time.Since(startedAt).Milliseconds()
	m.mu.Lock()
	now := time.Now()
	m.lastSweepAt = &now
	m.lastSweepDurationMs = durationMs
	m.mu.Unlock()
	return nil
}

func triggerDeviceHealthSweep() error {
	return globalDeviceHealthMonitor.runSweep()
}

func (m *deviceHealthMonitor) checkOne(deviceID uint32) {
	m.mu.RLock()
	state, ok := m.states[deviceID]
	cfg := m.cfg
	m.mu.RUnlock()
	if !ok {
		return
	}

	address := net.JoinHostPort(state.IP, strconv.Itoa(cfg.Port))
	checkStarted := time.Now()
	conn, err := net.DialTimeout("tcp", address, cfg.Timeout)
	durationMs := time.Since(checkStarted).Milliseconds()
	reachable := err == nil
	if conn != nil {
		_ = conn.Close()
	}

	now := time.Now()
	newStatus := state
	prevStatus := state.status()

	newStatus.ReachabilityKnown = true
	newStatus.Reachable = reachable
	newStatus.LastCheckAt = cloneTimePointer(&now)
	newStatus.LastCheckDurationMs = durationMs

	if reachable {
		newStatus.LastReachableAt = cloneTimePointer(&now)
		newStatus.ConsecutiveFailures = 0
		newStatus.LastCheckError = ""
	} else {
		newStatus.LastUnreachableAt = cloneTimePointer(&now)
		newStatus.ConsecutiveFailures++
		if err != nil {
			newStatus.LastCheckError = err.Error()
		} else {
			newStatus.LastCheckError = "errore sconosciuto"
		}
	}

	nextStatus := newStatus.status()
	stateChanged := prevStatus != nextStatus
	if stateChanged {
		newStatus.LastChangeAt = cloneTimePointer(&now)
	}

	m.mu.Lock()
	m.states[deviceID] = newStatus
	m.mu.Unlock()

	if stateChanged {
		reason := ""
		if reachable {
			reason = "reachability restored"
		} else if err != nil {
			reason = err.Error()
		} else {
			reason = "device unreachable"
		}

		trapAttempted := false
		trapSuccessful := false
		trapErrMsg := ""
		if prevStatus != "unknown" {
			trapAttempted = cfg.SnmpTrapEnabled
			if cfg.SnmpTrapEnabled {
				if trapErr := sendAnvizDeviceSNMPTrap(cfg, newStatus, prevStatus, nextStatus, reason); trapErr != nil {
					trapErrMsg = trapErr.Error()
					log.Printf("ANVIZ health trap error device=%d ip=%s from=%s to=%s err=%v", newStatus.DeviceID, newStatus.IP, prevStatus, nextStatus, trapErr)
				} else {
					trapSuccessful = true
				}
			}
		}

		log.Printf("ANVIZ health state-change device=%d ip=%s from=%s to=%s latency_ms=%d reason=%s", newStatus.DeviceID, newStatus.IP, prevStatus, nextStatus, durationMs, reason)

		m.appendEvent(SystemDeviceHealthEvent{
			Timestamp:      now,
			DeviceID:       int(newStatus.DeviceID),
			IP:             newStatus.IP,
			FromStatus:     prevStatus,
			ToStatus:       nextStatus,
			Reason:         reason,
			TrapAttempted:  trapAttempted,
			TrapSuccessful: trapSuccessful,
			TrapError:      trapErrMsg,
		})
	}
}

func (m *deviceHealthMonitor) appendEvent(event SystemDeviceHealthEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.events = append(m.events, event)
	if len(m.events) > m.cfg.EventBuffer {
		m.events = m.events[len(m.events)-m.cfg.EventBuffer:]
	}
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func sendAnvizDeviceSNMPTrap(cfg deviceHealthConfig, state deviceHealthState, fromStatus string, toStatus string, reason string) error {
	if !cfg.SnmpTrapEnabled {
		return nil
	}
	if cfg.SnmpTrapTarget == "" {
		return fmt.Errorf("SNMP_TRAP_TARGET non configurato")
	}

	trapOID := strings.TrimSuffix(cfg.SnmpTrapBaseOID, ".") + ".1"
	if toStatus == "online" {
		trapOID = strings.TrimSuffix(cfg.SnmpTrapBaseOID, ".") + ".2"
	}

	g := &gosnmp.GoSNMP{
		Target:    cfg.SnmpTrapTarget,
		Port:      uint16(cfg.SnmpTrapPort),
		Version:   gosnmp.Version2c,
		Community: cfg.SnmpTrapCommunity,
		Timeout:   3 * time.Second,
		Retries:   0,
	}

	if err := g.Connect(); err != nil {
		return fmt.Errorf("connect trap target failed: %w", err)
	}
	defer g.Conn.Close()

	trap := gosnmp.SnmpTrap{
		Variables: []gosnmp.SnmpPDU{
			{Name: ".1.3.6.1.6.3.1.1.4.1.0", Type: gosnmp.ObjectIdentifier, Value: trapOID},
			{Name: strings.TrimSuffix(cfg.SnmpTrapBaseOID, ".") + ".10.1", Type: gosnmp.Integer, Value: int(state.DeviceID)},
			{Name: strings.TrimSuffix(cfg.SnmpTrapBaseOID, ".") + ".10.2", Type: gosnmp.OctetString, Value: state.IP},
			{Name: strings.TrimSuffix(cfg.SnmpTrapBaseOID, ".") + ".10.3", Type: gosnmp.OctetString, Value: fromStatus},
			{Name: strings.TrimSuffix(cfg.SnmpTrapBaseOID, ".") + ".10.4", Type: gosnmp.OctetString, Value: toStatus},
			{Name: strings.TrimSuffix(cfg.SnmpTrapBaseOID, ".") + ".10.5", Type: gosnmp.OctetString, Value: reason},
			{Name: strings.TrimSuffix(cfg.SnmpTrapBaseOID, ".") + ".10.6", Type: gosnmp.OctetString, Value: time.Now().Format(time.RFC3339)},
		},
	}

	_, err := g.SendTrap(trap)
	if err != nil {
		return fmt.Errorf("send trap failed: %w", err)
	}

	return nil
}

func getDeviceHealthSummaryAndEvents() (SystemDeviceHealthSummary, map[uint32]deviceHealthState, []SystemDeviceHealthEvent) {
	m := globalDeviceHealthMonitor
	m.mu.RLock()
	defer m.mu.RUnlock()

	summary := SystemDeviceHealthSummary{
		MonitorEnabled:      m.cfg.Enabled,
		AutomaticMode:       m.cfg.Automatic,
		CheckMethod:         "tcp_connect",
		IntervalSeconds:     int(m.cfg.Interval / time.Second),
		TimeoutSeconds:      int(m.cfg.Timeout / time.Second),
		Port:                m.cfg.Port,
		IncludeInactive:     m.cfg.IncludeInactive,
		SnmpTrapEnabled:     m.cfg.SnmpTrapEnabled,
		SnmpTrapTarget:      m.cfg.SnmpTrapTarget,
		LastSweepDurationMs: m.lastSweepDurationMs,
		LastSweepAt:         cloneTimePointer(m.lastSweepAt),
	}

	states := make(map[uint32]deviceHealthState, len(m.states))
	for id, state := range m.states {
		states[id] = state
		switch state.status() {
		case "online":
			summary.OnlineCount++
		case "offline":
			summary.OfflineCount++
		default:
			summary.UnknownCount++
		}
	}

	events := make([]SystemDeviceHealthEvent, len(m.events))
	copy(events, m.events)
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.After(events[j].Timestamp)
	})

	return summary, states, events
}
