package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SyncStats struct {
	Received   int
	Inserted   int
	Duplicates int
	Errors     int
	HasWindow  bool
	Earliest   time.Time
	Latest     time.Time
}

type attendanceChunkRecord struct {
	UserID        uint32
	TimestampSecs uint32
	RecordTime    time.Time
	StatusCode    int
	Action        string
}

var deviceSyncGuards sync.Map

type syncRunConfig struct {
	RunID                string
	IP                   string
	DeviceID             uint32
	Manual               bool
	AttendanceMode       byte
	AttendanceLimit      byte
	SyncStaff            bool
	SyncAttendance       bool
	CommandDelay         time.Duration
	SyncInterval         time.Duration
	ReadTimeout          time.Duration
	WriteTimeout         time.Duration
	ConnectionTimeout    time.Duration
	StartedAt            time.Time
	StaffChunks          int
	AttendanceChunks     int
	LastSuccessfulChunk  int
	LastChunkRecordCount int
	LastChunkMode        byte
}

func (cfg syncRunConfig) prefix() string {
	mode := "auto"
	if cfg.Manual {
		mode = "manual"
	}
	return fmt.Sprintf("ANVIZ sync run=%s device=%d ip=%s mode=%s", cfg.RunID, cfg.DeviceID, cfg.IP, mode)
}

func (cfg syncRunConfig) logf(format string, args ...interface{}) {
	log.Printf("%s "+format, append([]interface{}{cfg.prefix()}, args...)...)
}

func (s *SyncStats) Add(other SyncStats) {
	s.Received += other.Received
	s.Inserted += other.Inserted
	s.Duplicates += other.Duplicates
	s.Errors += other.Errors
	if other.HasWindow {
		if !s.HasWindow || other.Earliest.Before(s.Earliest) {
			s.Earliest = other.Earliest
		}
		if !s.HasWindow || other.Latest.After(s.Latest) {
			s.Latest = other.Latest
		}
		s.HasWindow = true
	}
}

func (s *SyncStats) ObserveTimestamp(timestamp time.Time) {
	if !s.HasWindow || timestamp.Before(s.Earliest) {
		s.Earliest = timestamp
	}
	if !s.HasWindow || timestamp.After(s.Latest) {
		s.Latest = timestamp
	}
	s.HasWindow = true
}

func decodeAttendanceAction(recordBytes []byte) (int, string) {
	statusMap := map[int]string{
		0: "In",
		1: "Out",
		2: "I_pausa",
		3: "F_pausa",
		4: "U_trasf",
		5: "R_trasf",
		6: "I_break",
		7: "F_break",
	}

	statusCandidates := []int{}
	if len(recordBytes) > 9 {
		statusCandidates = append(statusCandidates, int(recordBytes[9]))
	}
	if len(recordBytes) > 8 {
		statusCandidates = append(statusCandidates, int(recordBytes[8]))
	}

	for _, candidate := range statusCandidates {
		if action, ok := statusMap[candidate]; ok {
			return candidate, normalizeAnvizDeviceRawAction(candidate, action)
		}
	}

	if len(statusCandidates) > 0 {
		return statusCandidates[0], fmt.Sprintf("unknown_%d", statusCandidates[0])
	}

	return -1, "unknown"
}

func useSimplifiedDeviceRawActions() bool {
	return envBool("ANVIZ_SIMPLIFIED_DEVICE_RAW_ACTIONS", false)
}

func normalizeAnvizDeviceRawAction(statusCode int, legacyAction string) string {
	if !useSimplifiedDeviceRawActions() {
		return legacyAction
	}

	switch statusCode {
	case 0, 4, 5:
		return "in_out"
	case 1, 2, 3, 6, 7:
		return "pausa"
	default:
		return legacyAction
	}
}

func sortAttendanceChunkRecordsChronologically(records []attendanceChunkRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].RecordTime.Equal(records[j].RecordTime) {
			if records[i].TimestampSecs == records[j].TimestampSecs {
				return records[i].UserID < records[j].UserID
			}
			return records[i].TimestampSecs < records[j].TimestampSecs
		}
		return records[i].RecordTime.Before(records[j].RecordTime)
	})
}

func summarizeAttendanceRecordBytes(recordBytes []byte) string {
	if len(recordBytes) == 0 {
		return ""
	}

	status8 := "n/a"
	status9 := "n/a"
	if len(recordBytes) > 8 {
		status8 = fmt.Sprintf("%d", int(recordBytes[8]))
	}
	if len(recordBytes) > 9 {
		status9 = fmt.Sprintf("%d", int(recordBytes[9]))
	}

	hexLen := len(recordBytes)
	if hexLen > 14 {
		hexLen = 14
	}

	return fmt.Sprintf("status8=%s status9=%s bytes=%s", status8, status9, hex.EncodeToString(recordBytes[:hexLen]))
}

func getDeviceSyncGuard(deviceID uint32) *sync.Mutex {
	guard, _ := deviceSyncGuards.LoadOrStore(deviceID, &sync.Mutex{})
	return guard.(*sync.Mutex)
}

func envInt(name string, defaultValue int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return defaultValue
	}

	return value
}

func envBool(name string, defaultValue bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}

	return raw != "0" && !strings.EqualFold(raw, "false") && !strings.EqualFold(raw, "no")
}

func parseAttendanceMode(raw string, defaultMode byte) byte {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "default":
		return defaultMode
	case "all", "all-records", "full":
		return 0x01
	case "new", "new-records", "recent":
		return 0x02
	case "next", "next-page":
		return 0x00
	default:
		return defaultMode
	}
}

func anvizCommandDelay() time.Duration {
	value := envInt("ANVIZ_COMMAND_DELAY_MS", 5000)
	if value < 0 {
		value = 5000
	}

	return time.Duration(value) * time.Millisecond
}

func pauseBetweenAnvizCommands(delay time.Duration) {
	if delay > 0 {
		time.Sleep(delay)
	}
}

func anvizReadTimeout() time.Duration {
	seconds := envInt("ANVIZ_READ_TIMEOUT_SECONDS", 20)
	if seconds <= 0 {
		seconds = 20
	}
	return time.Duration(seconds) * time.Second
}

func anvizWriteTimeout() time.Duration {
	seconds := envInt("ANVIZ_WRITE_TIMEOUT_SECONDS", 10)
	if seconds <= 0 {
		seconds = 10
	}
	return time.Duration(seconds) * time.Second
}

func anvizConnectionTimeout() time.Duration {
	seconds := envInt("ANVIZ_CONNECT_TIMEOUT_SECONDS", 5)
	if seconds <= 0 {
		seconds = 5
	}
	return time.Duration(seconds) * time.Second
}

func anvizSyncInterval() time.Duration {
	minutes := envInt("ANVIZ_SYNC_INTERVAL_MINUTES", 30)
	if minutes <= 0 {
		minutes = 30
	}
	return time.Duration(minutes) * time.Minute
}

func anvizAttendanceChunkLimit() byte {
	limit := envInt("ANVIZ_ATTENDANCE_CHUNK_LIMIT", 25)
	if limit <= 0 {
		limit = 25
	}
	if limit > 25 {
		limit = 25
	}
	return byte(limit)
}

func shouldSyncStaff(manual bool) bool {
	if manual {
		return envBool("ANVIZ_MANUAL_SYNC_STAFF", false)
	}
	return envBool("ANVIZ_AUTO_SYNC_STAFF", false)
}

func attendanceModeLabel(mode byte) string {
	switch mode {
	case 0x01:
		return "all-records"
	case 0x02:
		return "new-records"
	case 0x00:
		return "next-page"
	default:
		return fmt.Sprintf("unknown-0x%02X", mode)
	}
}

func useRecentFirstForDevice(deviceID uint32) bool {
	configured := strings.TrimSpace(os.Getenv("ANVIZ_RECENT_FIRST_DEVICES"))
	if configured == "" {
		return false
	}

	for _, token := range strings.Split(configured, ",") {
		value := strings.TrimSpace(token)
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			continue
		}
		if uint32(parsed) == deviceID {
			return true
		}
	}

	return false
}

func initialAttendanceMode(deviceID uint32, manual bool) byte {
	if manual {
		return parseAttendanceMode(os.Getenv("ANVIZ_MANUAL_ATTENDANCE_MODE"), 0x01)
	}

	if useRecentFirstForDevice(deviceID) {
		return 0x02
	}

	return parseAttendanceMode(os.Getenv("ANVIZ_AUTO_ATTENDANCE_MODE"), 0x02)
}

func formatActionHistogram(actionCounts map[string]int) string {
	if len(actionCounts) == 0 {
		return "none"
	}

	keys := make([]string, 0, len(actionCounts))
	for key := range actionCounts {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, actionCounts[key]))
	}

	return strings.Join(parts, ",")
}

const (
	AnvizSTX          = 0xA5
	AnvizCommand      = 0x40 // Comando TC_B (Download All Attendance Records) oppure 0x4C (Download New Attendance Records)
	AnvizCommandClear = 0x4E // Comando TC_C (Clear New Attendance Records)
)

// CCITT reversed table polynomial 0x8408 (Anviz table 0x1189 etc)
var ccitt_table = []uint16{
	0x0000, 0x1189, 0x2312, 0x329b, 0x4624, 0x57ad, 0x6536, 0x74bf,
	0x8c48, 0x9dc1, 0xaf5a, 0xbed3, 0xca6c, 0xdbe5, 0xe97e, 0xf8f7,
	0x1081, 0x0108, 0x3393, 0x221a, 0x56a5, 0x472c, 0x75b7, 0x643e,
	0x9cc9, 0x8d40, 0xbfdb, 0xae52, 0xdaed, 0xcb64, 0xf9ff, 0xe876,
	0x2102, 0x308b, 0x0210, 0x1399, 0x6726, 0x76af, 0x4434, 0x55bd,
	0xad4a, 0xbcc3, 0x8e58, 0x9fd1, 0xeb6e, 0xfae7, 0xc87c, 0xd9f5,
	0x3183, 0x200a, 0x1291, 0x0318, 0x77a7, 0x662e, 0x54b5, 0x453c,
	0xbdcb, 0xac42, 0x9ed9, 0x8f50, 0xfbef, 0xea66, 0xd8fd, 0xc974,
	0x4204, 0x538d, 0x6116, 0x709f, 0x0420, 0x15a9, 0x2732, 0x36bb,
	0xce4c, 0xdfc5, 0xed5e, 0xfcd7, 0x8868, 0x99e1, 0xab7a, 0xbaf3,
	0x5285, 0x430c, 0x7197, 0x601e, 0x14a1, 0x0528, 0x37b3, 0x263a,
	0xdecd, 0xcf44, 0xfddf, 0xec56, 0x98e9, 0x8960, 0xbbfb, 0xaa72,
	0x6306, 0x728f, 0x4014, 0x519d, 0x2522, 0x34ab, 0x0630, 0x17b9,
	0xef4e, 0xfec7, 0xcc5c, 0xddd5, 0xa96a, 0xb8e3, 0x8a78, 0x9bf1,
	0x7387, 0x620e, 0x5095, 0x411c, 0x35a3, 0x242a, 0x16b1, 0x0738,
	0xffcf, 0xee46, 0xdcdd, 0xcd54, 0xb9eb, 0xa862, 0x9af9, 0x8b70,
	0x8408, 0x9581, 0xa71a, 0xb693, 0xc22c, 0xd3a5, 0xe13e, 0xf0b7,
	0x0840, 0x19c9, 0x2b52, 0x3adb, 0x4e64, 0x5fed, 0x6d76, 0x7cff,
	0x9489, 0x8500, 0xb79b, 0xa612, 0xd2ad, 0xc324, 0xf1bf, 0xe036,
	0x18c1, 0x0948, 0x3bd3, 0x2a5a, 0x5ee5, 0x4f6c, 0x7df7, 0x6c7e,
	0xa50a, 0xb483, 0x8618, 0x9791, 0xe32e, 0xf2a7, 0xc03c, 0xd1b5,
	0x2942, 0x38cb, 0x0a50, 0x1bd9, 0x6f66, 0x7eef, 0x4c74, 0x5dfd,
	0xb58b, 0xa402, 0x9699, 0x8710, 0xf3af, 0xe226, 0xd0bd, 0xc134,
	0x39c3, 0x284a, 0x1ad1, 0x0b58, 0x7fe7, 0x6e6e, 0x5cf5, 0x4d7c,
	0xc60c, 0xd785, 0xe51e, 0xf497, 0x8028, 0x91a1, 0xa33a, 0xb2b3,
	0x4a44, 0x5bcd, 0x6956, 0x78df, 0x0c60, 0x1de9, 0x2f72, 0x3efb,
	0xd68d, 0xc704, 0xf59f, 0xe416, 0x90a9, 0x8120, 0xb3bb, 0xa232,
	0x5ac5, 0x4b4c, 0x79d7, 0x685e, 0x1ce1, 0x0d68, 0x3ff3, 0x2e7a,
	0xe70e, 0xf687, 0xc41c, 0xd595, 0xa12a, 0xb0a3, 0x8238, 0x93b1,
	0x6b46, 0x7acf, 0x4854, 0x59dd, 0x2d62, 0x3ceb, 0x0e70, 0x1ff9,
	0xf78f, 0xe606, 0xd49d, 0xc514, 0xb1ab, 0xa022, 0x92b9, 0x8330,
	0x7bc7, 0x6a4e, 0x58d5, 0x495c, 0x3de3, 0x2c6a, 0x1ef1, 0x0f78,
}

// crc16 elabora il CRC Anviz per validare o comporre blocchi dati validi TCP.
func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		crc = (crc >> 8) ^ ccitt_table[crc&0xFF]
	}
	return crc
}

// BuildAnvizPacket costruisce i byte raw da inviare all'Anviz.
// Formato: STX + Device ID + Command + Length + Data + CRC16
func BuildAnvizPacket(deviceID uint32, command byte, data []byte) []byte {
	var buf bytes.Buffer

	// 1. STX (1 byte)
	buf.WriteByte(AnvizSTX)

	// 2. Device ID (4 bytes, protocollo Anviz lo prevede Big Endian tipicamente)
	devIDBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(devIDBytes, deviceID)
	buf.Write(devIDBytes)

	// 3. Command (1 byte)
	buf.WriteByte(command)

	// 4. Length (2 bytes, Big Endian)
	lenBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBytes, uint16(len(data)))
	buf.Write(lenBytes)

	// 5. Data Variable Payload
	if len(data) > 0 {
		buf.Write(data)
	}

	// 6. CRC16 computation over STX, DevID, CMD, LEN, Data. Scritto tipicamente in Little Endian da Anviz
	crc := crc16(buf.Bytes())
	crcBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(crcBytes, crc)
	buf.Write(crcBytes)

	return buf.Bytes()
}

// readFullAnvizPacket legge deterministicamente un pacchetto in arrivo risolvendo l'eventuale frammentazione TCP
func readFullAnvizPacket(conn net.Conn, timeout time.Duration) []byte {
	// Il pacchetto di base ha sempre 9 byte di Header prima dei Dati e del CRC.
	// STX(1) + ID(4) + ACK(1) + RET(1) + LEN(2) = 9
	header := make([]byte, 9)
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil
	}

	if header[0] != AnvizSTX {
		return nil
	}

	// La lunghezza è all'indice 7 e 8
	dataLen := binary.BigEndian.Uint16(header[7:9])

	// Fallback se il firmware usa LittleEndian o ci sono glitch
	if dataLen > 40000 {
		dataLen = binary.LittleEndian.Uint16(header[7:9])
	}
	if dataLen > 40000 {
		// Probabile corruzione, ritorno solo l'header per debug o annullo
		return nil
	}

	// Leggiamo la parte rimanente (Data + 2 bytes di CRC)
	rest := make([]byte, dataLen+2)
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	if _, err := io.ReadFull(conn, rest); err != nil {
		return nil
	}

	return append(header, rest...)
}

// parseAnvizResponse accetta il byte buffer di ritorno dal socket e lo smonta.
// Per i record presenze restituisce le statistiche di ingestione del chunk.
func parseAnvizResponse(res []byte, ip string, deviceID uint32) SyncStats {
	stats := SyncStats{}

	if len(res) < 11 {
		stats.Errors++
		return stats
	}

	cmdResponse := res[5]

	// Lunghezza del pacchetto è ad indice 7 e 8
	dataLen := binary.BigEndian.Uint16(res[7:9])
	if dataLen > 40000 {
		dataLen = binary.LittleEndian.Uint16(res[7:9])
	}

	// RET Code è ad indice 6. Solitamente 0x00 significa "successo".
	retCode := res[6]
	if retCode != 0x00 {
		log.Printf("TCP Worker: Il dispositivo Anviz ha risposto con codice di errore (RET: 0x%X).\n", retCode)
		if dataLen == 0 {
			stats.Errors++
			return stats
		}
	}

	// Body dati reale inizia ad indice 9 e finisce ad indice 9+length
	if len(res) < int(9+dataLen) {
		stats.Errors++
		return stats
	}
	data := res[9 : 9+dataLen]

	if len(data) == 0 {
		return stats
	}

	recordCount := int(data[0])

	if recordCount == 0 {
		return stats
	}

	// Comando di Ritorno per Timbrature (ACK di 0x40 - Download New Attendance) = 0xC0 (0x40 + 0x80)
	// Comando di Ritorno per Staff Info (ACK di 0x72 - Download Staff Info) = 0xF2 (0x72 + 0x80)

	if cmdResponse == 0xC0 {
		log.Printf("TCP Worker: Parsing %d timbrature dall'hardware Anviz %s.", recordCount, ip)
		stats.Received += recordCount
		actionCounts := map[string]int{}
		chunkRecords := make([]attendanceChunkRecord, 0, recordCount)

		validRecordCount := int(data[0]) // Il primo byte è il "count"
		idx := 2                         // Nei pacchetti 0x40 il payload inizia da byte indice 2 (dopo i due header count)

		for i := 0; i < validRecordCount; i++ {
			remaining := len(data) - idx
			if remaining <= 0 {
				log.Printf("TCP Worker: Chunk parse truncato device=%d ip=%s idx=%d len(data)=%d expected_remaining=%d", deviceID, ip, idx, len(data), validRecordCount-i)
				stats.Errors += validRecordCount - i
				break
			}

			recordLen := 14
			recordBytes := make([]byte, recordLen)
			if remaining >= recordLen {
				copy(recordBytes, data[idx:idx+recordLen])
				idx += recordLen
			} else if remaining == recordLen-1 && validRecordCount-i == 1 {
				copy(recordBytes, data[idx:])
				idx = len(data)
				log.Printf("TCP Worker: Chunk tail incompleto recuperato device=%d ip=%s idx=%d len(data)=%d missing_bytes=%d", deviceID, ip, idx, len(data), recordLen-remaining)
			} else {
				log.Printf("TCP Worker: Chunk parse truncato device=%d ip=%s idx=%d len(data)=%d expected_remaining=%d remaining_bytes=%d", deviceID, ip, idx, len(data), validRecordCount-i, remaining)
				stats.Errors += validRecordCount - i
				break
			}

			// ID 4 byte (byte 0..3)
			userID := binary.BigEndian.Uint32(recordBytes[0:4])
			// Time 4 byte (byte 4..7) - L'Anviz memorizza i secondi dall'epoca 2000-01-02 nell'orario LOCALE del dispositivo
			timestampSecs := binary.BigEndian.Uint32(recordBytes[4:8])
			recordTime := anvizRawTimestampToTime(timestampSecs)
			stats.ObserveTimestamp(recordTime)

			// Nei pacchetti TC_B il codice stato presenze e` normalmente nel backup/status byte.
			// Manteniamo un fallback all'offset legacy per compatibilita` con firmware differenti.
			statusCode, action := decodeAttendanceAction(recordBytes)
			actionCounts[action]++
			log.Printf("TCP Worker: Attendance record scaricato device=%d ip=%s employee=%d raw_ts=%d parsed_ts=%s action=%s status=%d %s", deviceID, ip, userID, timestampSecs, recordTime.Format(time.RFC3339), action, statusCode, summarizeAttendanceRecordBytes(recordBytes))

			chunkRecords = append(chunkRecords, attendanceChunkRecord{
				UserID:        userID,
				TimestampSecs: timestampSecs,
				RecordTime:    recordTime,
				StatusCode:    statusCode,
				Action:        action,
			})
		}

		sortAttendanceChunkRecordsChronologically(chunkRecords)

		for _, chunkRecord := range chunkRecords {
			employeeName := GetEmployeeName(int(chunkRecord.UserID))
			if employeeName == "" {
				employeeName = fmt.Sprintf("Utente %d", chunkRecord.UserID)
			}

			inserted, err := InsertDeviceRecord(deviceID, int(chunkRecord.UserID), employeeName, chunkRecord.RecordTime, chunkRecord.TimestampSecs, chunkRecord.Action, chunkRecord.StatusCode)
			if err == ErrDuplicateRecord {
				stats.Duplicates++
				log.Printf("TCP Worker: Timbratura device gia presente, salto device=%d employee=%d raw_ts=%d status=%d", deviceID, chunkRecord.UserID, chunkRecord.TimestampSecs, chunkRecord.StatusCode)
				continue
			}
			if err != nil {
				stats.Errors++
				log.Printf("TCP Worker: Errore insert SQLite timbratura device id %d: %v", chunkRecord.UserID, err)
				continue
			}
			if !inserted {
				stats.Duplicates++
				log.Printf("TCP Worker: Timbratura device gia presente, salto device=%d employee=%d raw_ts=%d status=%d", deviceID, chunkRecord.UserID, chunkRecord.TimestampSecs, chunkRecord.StatusCode)
				continue
			}

			stats.Inserted++
		}

		if stats.HasWindow {
			log.Printf("TCP Worker: Attendance chunk summary device=%d ip=%s earliest=%s latest=%s actions=%s", deviceID, ip, stats.Earliest.Format(time.RFC3339), stats.Latest.Format(time.RFC3339), formatActionHistogram(actionCounts))
		} else {
			log.Printf("TCP Worker: Attendance chunk summary device=%d ip=%s no-parseable-timestamps actions=%s", deviceID, ip, formatActionHistogram(actionCounts))
		}
	} else if cmdResponse == 0xF2 {
		log.Printf("TCP Worker: Parsing %d Profili Dipendenti dall'hardware Anviz %s.", recordCount, ip)

		idx := 1
		// 0x72 staff info payloads weigh usually 30/40 bytes each minimum depending on name length
		for i := 0; i < recordCount; i++ {
			// Per Anviz standard, Download Staff invia packet chunk di circa 40 byte
			// Byte 0-4: ID, Byte 12-14 o 8-11: PIN, etc (struttura variabile, usiamo approccio prudente su 40 byte)
			if idx+30 > len(data) {
				break
			}
			profileBytes := data[idx : idx+40]
			idx += 40

			// ID (5 byte: 0 a 4) - Di solito i primi 4 bastano per ID < 4,294,967,295, ma l'Anviz legge 5
			// Prendo gli ultimi 4 byte per l'intero e lo sommo
			userID := (uint64(profileBytes[0]) << 32) | (uint64(profileBytes[1]) << 24) | (uint64(profileBytes[2]) << 16) | (uint64(profileBytes[3]) << 8) | uint64(profileBytes[4])

			// PIN (3 byte: 5 a 7)
			var pinStr string
			if profileBytes[5] == 0xFF && profileBytes[6] == 0xFF && profileBytes[7] == 0xFF {
				pinStr = ""
			} else {
				// Lunghezza dal nibble superiore del byte 5
				passLen := int(profileBytes[5] >> 4)
				// Valore dal resto
				passVal := (uint32(profileBytes[5]&0x0F) << 16) | (uint32(profileBytes[6]) << 8) | uint32(profileBytes[7])
				pinStr = fmt.Sprintf("%0*d", passLen, passVal)
			}

			// Card ID (4 byte: 8 a 11)
			var cardID uint32
			if profileBytes[8] == 0xFF && profileBytes[9] == 0xFF && profileBytes[10] == 0xFF && profileBytes[11] == 0xFF {
				cardID = 0
			} else {
				cardID = binary.BigEndian.Uint32(profileBytes[8:12])
			}

			// Name (20 byte: 12 a 31) - E' una stringa che può avere null bytes interni per via del padding
			nameBytes := profileBytes[12:32]
			var cleanName []byte
			for _, b := range nameBytes {
				if b != 0x00 {
					cleanName = append(cleanName, b)
				}
			}
			nameStr := string(bytes.TrimSpace(cleanName))
			if nameStr == "" {
				nameStr = fmt.Sprintf("Nome vuoto (ID: %d)", userID)
			}

			err := SyncEmployee(int(userID), nameStr, pinStr)
			if err != nil {
				log.Printf("TCP Worker: Errore sincronizzazione dipendente ID %d: %v", userID, err)
			} else {
				log.Printf("TCP Worker: Dipendente ID %d salvato. PIN: %s, Card: %d, Nome: %s", userID, pinStr, cardID, nameStr)
			}
		}
	}

	return stats
}

// SyncAnvizWorker è il worker che fa il polling all'hardware a scadenza temporale.
func SyncAnvizWorker(ip string, deviceID uint32) {
	interval := anvizSyncInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Tentativo di sync iniziale all'avvio dell'app!
	_, _ = syncFromDevice(ip, deviceID, false)

	for {
		<-ticker.C
		log.Printf("ANVIZ worker device=%d ip=%s scheduling automatic sync interval=%s", deviceID, ip, interval)
		_, _ = syncFromDevice(ip, deviceID, false)
	}
}

// syncFromDevice esegue una socket net.Dial vera verso l'hardware e implementa timeout in caso esso sia offline
func syncFromDeviceLegacy(ip string, deviceID uint32, manual bool) (SyncStats, error) {
	stats := SyncStats{}
	guard := getDeviceSyncGuard(deviceID)
	guard.Lock()
	defer guard.Unlock()

	log.Printf("TCP Worker: Tentativo di sincronizzazione con l'Anviz IP %s...", ip)

	// Usiamo DialTimeout per non bloccare la Goroutine per sempre se Anviz è spento\network issue.
	conn, err := net.DialTimeout("tcp", ip+":5010", 5*time.Second)
	if err != nil {
		log.Printf("TCP Worker: l'Anviz (%s) sembra spento e irraggiungibile: %v", ip, err)
		return stats, fmt.Errorf("dispositivo %s irraggiungibile", ip)
	}
	defer conn.Close()

	// --- 1. Login Authentication (Command 0x38) ---
	// La password fornita è vuota (0). Protocollo Anviz tipicamente usa Little Endian per i payload DATA numerici.
	pwdZero := make([]byte, 4) // Password di default/vuota
	loginPacket := BuildAnvizPacket(deviceID, 0x38, pwdZero)
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(loginPacket); err != nil {
		log.Printf("TCP Worker: Errore durante tcp write (login): %v", err)
		return stats, fmt.Errorf("errore comunicazione login %s", ip)
	}

	// Leggiamo la risposta del login
	loginRes := readFullAnvizPacket(conn, anvizReadTimeout())
	if loginRes == nil {
		log.Printf("TCP Worker: Timeout lettura login Anviz %s", ip)
		return stats, fmt.Errorf("timeout login %s", ip)
	}
	// Se la risposta al login (indice 6 RET Code) non è 0x00, proseguiamo comunque (su alcuni FW la pwd vuota non serve login)
	if loginRes[6] != 0x00 {
		log.Printf("TCP Worker: Login non riuscito con password vuota su %s (RET: 0x%X). Proseguo comunque...", ip, loginRes[6])
	} else {
		log.Printf("TCP Worker: Login riuscito su %s!", ip)
	}
	pauseBetweenAnvizCommands(anvizCommandDelay())

	// --- 1. Scaricamento Profili Staff (Command 0x72) ---
	staffMode := byte(0x01) // 0x01 per iniziare, 0x00 per le pagine successive
	for {
		staffReqData := []byte{staffMode}
		staffPacket := BuildAnvizPacket(deviceID, 0x72, staffReqData)
		log.Printf("TCP Worker: Request staff chunk device=%d ip=%s mode=0x%02X payload_len=%d", deviceID, ip, staffMode, len(staffReqData))

		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := conn.Write(staffPacket); err != nil {
			log.Printf("TCP Worker: Errore durante tcp write (staff): %v", err)
			break
		}

		staffRes := readFullAnvizPacket(conn, anvizReadTimeout())
		if staffRes != nil && staffRes[6] == 0x00 { // 0x00 Success
			count := int(staffRes[9])
			_ = parseAnvizResponse(staffRes, ip, deviceID)
			pauseBetweenAnvizCommands(anvizCommandDelay())

			if count < 8 { // L'Anviz tipicamente invia blocchi da 8 per lo staff
				break
			}
			staffMode = 0x00 // Richiedi la pagina successiva
		} else {
			if staffRes != nil {
				log.Printf("TCP Worker: Errore o fine record 0x72 %s (RET: 0x%X)", ip, staffRes[6])
			} else {
				log.Printf("TCP Worker: Timeout o pacchetto 0x72 corrotto %s", ip)
			}
			break
		}
	}

	// --- 2. Scaricamento Presenze (Command 0x40) ---
	// Mode: 1 (All Records), 2 (New Records), 0 (Next chunk of previous command)
	// Il protocollo non espone un vero reverse-order: il massimo che possiamo fare per i device lenti
	// e` usare "New Records" sul polling automatico e tenere "All Records" per il recupero manuale.
	mode := initialAttendanceMode(deviceID, manual)
	log.Printf("TCP Worker: Attendance sync strategy device=%d ip=%s initial_mode=0x%02X (%s) manual=%v", deviceID, ip, mode, attendanceModeLabel(mode), manual)
	limit := byte(0x19) // 25 records alla volta (max supportato da molti vecchi firmware in un colpo)
	chunkIndex := 0

	for {
		chunkIndex++
		requestMode := mode
		reqData := []byte{mode, limit}
		packet := BuildAnvizPacket(deviceID, 0x40, reqData)
		log.Printf("TCP Worker: Request attendance chunk device=%d ip=%s chunk=%d mode=0x%02X (%s) limit=%d", deviceID, ip, chunkIndex, mode, attendanceModeLabel(mode), limit)

		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := conn.Write(packet); err != nil {
			log.Printf("TCP Worker: Errore durante tcp write (dati): %v", err)
			break
		}

		recordRes := readFullAnvizPacket(conn, anvizReadTimeout())
		if recordRes != nil && recordRes[6] == 0x00 { // 0x00 Success
			count := int(recordRes[9])
			responseDataLen := binary.BigEndian.Uint16(recordRes[7:9])
			if responseDataLen > 40000 {
				responseDataLen = binary.LittleEndian.Uint16(recordRes[7:9])
			}
			log.Printf("TCP Worker: Attendance chunk ack device=%d ip=%s chunk=%d request_mode=0x%02X (%s) ack=0x%02X ret=0x%02X data_len=%d count=%d", deviceID, ip, chunkIndex, requestMode, attendanceModeLabel(requestMode), recordRes[5], recordRes[6], responseDataLen, count)
			chunkStats := parseAnvizResponse(recordRes, ip, deviceID)
			stats.Add(chunkStats)
			windowSummary := "no-window"
			if chunkStats.HasWindow {
				windowSummary = fmt.Sprintf("%s -> %s", chunkStats.Earliest.Format(time.RFC3339), chunkStats.Latest.Format(time.RFC3339))
			}
			log.Printf("TCP Worker: Chunk %s chunk=%d ricevuti=%d nuovi=%d duplicati=%d errori=%d window=%s", ip, chunkIndex, chunkStats.Received, chunkStats.Inserted, chunkStats.Duplicates, chunkStats.Errors, windowSummary)

			if requestMode == 0x02 {
				if count == 0 {
					log.Printf("TCP Worker: NEW-RECORDS diagnostica device=%d ip=%s chunk=%d count=0 -> il device non sta esponendo nuove timbrature via puntatore interno", deviceID, ip, chunkIndex)
				} else if chunkStats.HasWindow {
					staleThreshold := time.Now().AddDate(0, 0, -7)
					if chunkStats.Latest.Before(staleThreshold) {
						log.Printf("TCP Worker: NEW-RECORDS diagnostica device=%d ip=%s chunk=%d latest=%s troppo vecchio rispetto a now=%s -> puntatore new-records presumibilmente incoerente", deviceID, ip, chunkIndex, chunkStats.Latest.Format(time.RFC3339), time.Now().Format(time.RFC3339))
					} else {
						log.Printf("TCP Worker: NEW-RECORDS diagnostica device=%d ip=%s chunk=%d latest=%s coerente con record recenti", deviceID, ip, chunkIndex, chunkStats.Latest.Format(time.RFC3339))
					}
				}
			}
			pauseBetweenAnvizCommands(anvizCommandDelay())

			// Se il server ci ha restituito meno di 25 record, significa che li abbiamo esauriti tutti
			if count < 25 {
				break
			}
			// Per i pacchetti successivi la mode deve diventare 0 (Next Page)
			mode = 0x00
		} else {
			if recordRes != nil {
				log.Printf("TCP Worker: Errore scaricamento record (RET: 0x%X)", recordRes[6])
			} else {
				log.Printf("TCP Worker: Timeout o pacchetto corrotto durante scaricamento record")
			}
			break
		}
	}

	log.Printf("TCP Worker: Sync completata %s ricevuti=%d nuovi=%d duplicati=%d errori=%d", ip, stats.Received, stats.Inserted, stats.Duplicates, stats.Errors)
	return stats, nil
}

func syncFromDeviceWithOptions(ip string, deviceID uint32, manual bool, syncStaff bool, syncAttendance bool) (SyncStats, error) {
	stats := SyncStats{}
	cfg := syncRunConfig{
		RunID:             fmt.Sprintf("%d", time.Now().UnixNano()),
		IP:                ip,
		DeviceID:          deviceID,
		Manual:            manual,
		AttendanceMode:    initialAttendanceMode(deviceID, manual),
		AttendanceLimit:   anvizAttendanceChunkLimit(),
		SyncStaff:         syncStaff,
		SyncAttendance:    syncAttendance,
		CommandDelay:      anvizCommandDelay(),
		SyncInterval:      anvizSyncInterval(),
		ReadTimeout:       anvizReadTimeout(),
		WriteTimeout:      anvizWriteTimeout(),
		ConnectionTimeout: anvizConnectionTimeout(),
		StartedAt:         time.Now(),
	}

	guard := getDeviceSyncGuard(deviceID)
	cfg.logf("waiting for sync lock")
	guard.Lock()
	defer guard.Unlock()

	cfg.logf("starting sync attendance_mode=%s chunk_limit=%d sync_staff=%v sync_attendance=%v command_delay=%s read_timeout=%s write_timeout=%s connect_timeout=%s interval=%s",
		attendanceModeLabel(cfg.AttendanceMode), cfg.AttendanceLimit, cfg.SyncStaff, cfg.SyncAttendance, cfg.CommandDelay, cfg.ReadTimeout, cfg.WriteTimeout, cfg.ConnectionTimeout, cfg.SyncInterval)

	defer func() {
		duration := time.Since(cfg.StartedAt)
		windowSummary := "no-window"
		if stats.HasWindow {
			windowSummary = fmt.Sprintf("%s -> %s", stats.Earliest.Format(time.RFC3339), stats.Latest.Format(time.RFC3339))
		}
		cfg.logf("finished sync duration=%s staff_chunks=%d attendance_chunks=%d last_successful_chunk=%d last_chunk_records=%d last_chunk_mode=%s received=%d inserted=%d duplicates=%d errors=%d window=%s",
			duration.Round(time.Millisecond), cfg.StaffChunks, cfg.AttendanceChunks, cfg.LastSuccessfulChunk, cfg.LastChunkRecordCount, attendanceModeLabel(cfg.LastChunkMode), stats.Received, stats.Inserted, stats.Duplicates, stats.Errors, windowSummary)
	}()

	conn, err := net.DialTimeout("tcp", ip+":5010", cfg.ConnectionTimeout)
	if err != nil {
		cfg.logf("device unreachable err=%v", err)
		return stats, fmt.Errorf("dispositivo %s irraggiungibile", ip)
	}
	defer conn.Close()

	pwdZero := make([]byte, 4)
	loginPacket := BuildAnvizPacket(deviceID, 0x38, pwdZero)
	_ = conn.SetWriteDeadline(time.Now().Add(cfg.WriteTimeout))
	if _, err := conn.Write(loginPacket); err != nil {
		cfg.logf("login write error err=%v", err)
		return stats, fmt.Errorf("errore comunicazione login %s", ip)
	}

	loginRes := readFullAnvizPacket(conn, cfg.ReadTimeout)
	if loginRes == nil {
		cfg.logf("login read timeout/corrupted packet")
		return stats, fmt.Errorf("timeout login %s", ip)
	}
	if loginRes[6] != 0x00 {
		cfg.logf("login returned non-zero ret=0x%X, continuing for compatibility", loginRes[6])
	} else {
		cfg.logf("login successful")
	}
	pauseBetweenAnvizCommands(cfg.CommandDelay)

	if cfg.SyncStaff {
		staffMode := byte(0x01)
		for {
			cfg.StaffChunks++
			staffReqData := []byte{staffMode}
			staffPacket := BuildAnvizPacket(deviceID, 0x72, staffReqData)
			cfg.logf("requesting staff chunk=%d mode=%s", cfg.StaffChunks, attendanceModeLabel(staffMode))

			_ = conn.SetWriteDeadline(time.Now().Add(cfg.WriteTimeout))
			if _, err := conn.Write(staffPacket); err != nil {
				cfg.logf("staff write error chunk=%d err=%v", cfg.StaffChunks, err)
				break
			}

			staffRes := readFullAnvizPacket(conn, cfg.ReadTimeout)
			if staffRes != nil && staffRes[6] == 0x00 {
				count := int(staffRes[9])
				_ = parseAnvizResponse(staffRes, ip, deviceID)
				cfg.logf("staff chunk ack chunk=%d count=%d ret=0x%X", cfg.StaffChunks, count, staffRes[6])
				pauseBetweenAnvizCommands(cfg.CommandDelay)

				if count < 8 {
					break
				}
				staffMode = 0x00
			} else {
				if staffRes != nil {
					cfg.logf("staff chunk failed chunk=%d ret=0x%X", cfg.StaffChunks, staffRes[6])
				} else {
					cfg.logf("staff chunk timeout/corrupted packet chunk=%d", cfg.StaffChunks)
				}
				break
			}
		}
	} else {
		cfg.logf("staff sync skipped for this run")
	}

	if !cfg.SyncAttendance {
		cfg.logf("attendance sync skipped for this run")
		return stats, nil
	}

	mode := cfg.AttendanceMode
	limit := cfg.AttendanceLimit

	for chunkIndex := 1; ; chunkIndex++ {
		cfg.AttendanceChunks = chunkIndex
		cfg.LastChunkMode = mode

		reqData := []byte{mode, limit}
		packet := BuildAnvizPacket(deviceID, 0x40, reqData)
		cfg.logf("requesting attendance chunk=%d mode=%s limit=%d", chunkIndex, attendanceModeLabel(mode), limit)

		_ = conn.SetWriteDeadline(time.Now().Add(cfg.WriteTimeout))
		if _, err := conn.Write(packet); err != nil {
			cfg.logf("attendance write error chunk=%d err=%v", chunkIndex, err)
			break
		}

		recordRes := readFullAnvizPacket(conn, cfg.ReadTimeout)
		if recordRes == nil {
			cfg.logf("attendance chunk timeout/corrupted packet chunk=%d", chunkIndex)
			break
		}
		if recordRes[6] != 0x00 {
			cfg.logf("attendance chunk failed chunk=%d ret=0x%X", chunkIndex, recordRes[6])
			break
		}

		count := int(recordRes[9])
		cfg.LastChunkRecordCount = count
		cfg.LastSuccessfulChunk = chunkIndex

		responseDataLen := binary.BigEndian.Uint16(recordRes[7:9])
		if responseDataLen > 40000 {
			responseDataLen = binary.LittleEndian.Uint16(recordRes[7:9])
		}
		cfg.logf("attendance chunk ack chunk=%d mode=%s ack=0x%02X ret=0x%02X data_len=%d count=%d",
			chunkIndex, attendanceModeLabel(cfg.LastChunkMode), recordRes[5], recordRes[6], responseDataLen, count)

		chunkStats := parseAnvizResponse(recordRes, ip, deviceID)
		stats.Add(chunkStats)
		windowSummary := "no-window"
		if chunkStats.HasWindow {
			windowSummary = fmt.Sprintf("%s -> %s", chunkStats.Earliest.Format(time.RFC3339), chunkStats.Latest.Format(time.RFC3339))
		}
		cfg.logf("attendance chunk summary chunk=%d received=%d inserted=%d duplicates=%d errors=%d window=%s",
			chunkIndex, chunkStats.Received, chunkStats.Inserted, chunkStats.Duplicates, chunkStats.Errors, windowSummary)

		if cfg.LastChunkMode == 0x02 {
			if count == 0 {
				cfg.logf("new-records diagnostic chunk=%d count=0 device did not expose fresh records", chunkIndex)
			} else if chunkStats.HasWindow {
				staleThreshold := time.Now().AddDate(0, 0, -7)
				if chunkStats.Latest.Before(staleThreshold) {
					cfg.logf("new-records diagnostic chunk=%d latest=%s older than threshold now=%s pointer likely stale",
						chunkIndex, chunkStats.Latest.Format(time.RFC3339), time.Now().Format(time.RFC3339))
				} else {
					cfg.logf("new-records diagnostic chunk=%d latest=%s consistent with recent data",
						chunkIndex, chunkStats.Latest.Format(time.RFC3339))
				}
			}
		}

		pauseBetweenAnvizCommands(cfg.CommandDelay)

		if count < int(limit) {
			break
		}
		mode = 0x00
	}

	return stats, nil
}

func syncFromDevice(ip string, deviceID uint32, manual bool) (SyncStats, error) {
	return syncFromDeviceWithOptions(ip, deviceID, manual, shouldSyncStaff(manual), true)
}
