package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

const (
	AnvizSTX     = 0xA5
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
func readFullAnvizPacket(conn net.Conn) []byte {
	// Il pacchetto di base ha sempre 9 byte di Header prima dei Dati e del CRC.
	// STX(1) + ID(4) + ACK(1) + RET(1) + LEN(2) = 9
	header := make([]byte, 9)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
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
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.ReadFull(conn, rest); err != nil {
		return nil
	}

	return append(header, rest...)
}

// parseAnvizResponse accetta il byte buffer di ritorno dal socket e lo smonta.
func parseAnvizResponse(res []byte, ip string) {
	if len(res) < 11 {
		return
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
			return
		}
	}

	// Body dati reale inizia ad indice 9 e finisce ad indice 9+length
	if len(res) < int(9+dataLen) {
		return
	}
	data := res[9 : 9+dataLen]

	if len(data) == 0 {
		return
	}

	recordCount := int(data[0])

	if recordCount == 0 {
		return
	}

	// Comando di Ritorno per Timbrature (ACK di 0x40 - Download New Attendance) = 0xC0 (0x40 + 0x80)
	// Comando di Ritorno per Staff Info (ACK di 0x72 - Download Staff Info) = 0xF2 (0x72 + 0x80)

	if cmdResponse == 0xC0 {
		log.Printf("TCP Worker: Parsing %d timbrature dall'hardware Anviz %s.", recordCount, ip)
		
		validRecordCount := int(data[0]) // Il primo byte è il "count"
		idx := 2 // Nei pacchetti 0x40 il payload inizia da byte indice 2 (dopo i due header count)
		
		for i := 0; i < validRecordCount; i++ {
			if idx+14 > len(data) {
				break
			}
			recordBytes := data[idx : idx+14]
			idx += 14

			// ID 4 byte (byte 0..3)
			userID := binary.BigEndian.Uint32(recordBytes[0:4])
			// Time 4 byte (byte 4..7) - L'Anviz memorizza i secondi dall'epoca 2000-01-02 nell'orario LOCALE del dispositivo
			timestampSecs := binary.BigEndian.Uint32(recordBytes[4:8])
			localTZ, _ := time.LoadLocation("Europe/Rome")
			anvizEpoch := time.Date(2000, 1, 2, 0, 0, 0, 0, localTZ)
			recordTime := anvizEpoch.Add(time.Duration(timestampSecs) * time.Second)

			// Status byte (offset 9): codice di stato presenze 0-7
			statusCode := int(recordBytes[9])
			statusMap := map[int]string{
				0: "In",
				1: "Out",
				2: "I_pausa",
				3: "F_pausa",
				4: "U_trasf",
				5: "R_trasf",
				6: "I_pausa",
				7: "F_pausa",
			}
			action := statusMap[statusCode]
			if action == "" {
				action = fmt.Sprintf("unknown_%d", statusCode)
			}

			employeeName := GetEmployeeName(int(userID))
			if employeeName == "" {
				employeeName = fmt.Sprintf("Utente %d", userID)
			}

			err := InsertRecord(int(userID), employeeName, recordTime, action, statusCode, "device", nil, nil)
			if err != nil {
				log.Printf("TCP Worker: Errore insert SQLite timbratura device id %d: %v", userID, err)
			}
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
}

// SyncAnvizWorker è il worker che fa il polling all'hardware a scadenza temporale.
func SyncAnvizWorker(ip string, deviceID uint32) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	// Tentativo di sync iniziale all'avvio dell'app!
	_ = syncFromDevice(ip, deviceID)

	for {
		<-ticker.C
		_ = syncFromDevice(ip, deviceID)
	}
}

// syncFromDevice esegue una socket net.Dial vera verso l'hardware e implementa timeout in caso esso sia offline
func syncFromDevice(ip string, deviceID uint32) error {
	log.Printf("TCP Worker: Tentativo di sincronizzazione con l'Anviz IP %s...", ip)

	// Usiamo DialTimeout per non bloccare la Goroutine per sempre se Anviz è spento\network issue.
	conn, err := net.DialTimeout("tcp", ip+":5010", 5*time.Second)
	if err != nil {
		log.Printf("TCP Worker: l'Anviz (%s) sembra spento e irraggiungibile: %v", ip, err)
		return fmt.Errorf("dispositivo %s irraggiungibile", ip)
	}
	defer conn.Close()

	// --- 1. Login Authentication (Command 0x38) ---
	// La password fornita è vuota (0). Protocollo Anviz tipicamente usa Little Endian per i payload DATA numerici.
	pwdZero := make([]byte, 4) // Password di default/vuota
	loginPacket := BuildAnvizPacket(deviceID, 0x38, pwdZero)
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(loginPacket); err != nil {
		log.Printf("TCP Worker: Errore durante tcp write (login): %v", err)
		return fmt.Errorf("errore comunicazione login %s", ip)
	}

	// Leggiamo la risposta del login
	loginRes := readFullAnvizPacket(conn)
	if loginRes == nil {
		log.Printf("TCP Worker: Timeout lettura login Anviz %s", ip)
		return fmt.Errorf("timeout login %s", ip)
	}
	// Se la risposta al login (indice 6 RET Code) non è 0x00, proseguiamo comunque (su alcuni FW la pwd vuota non serve login)
	if loginRes[6] != 0x00 {
		log.Printf("TCP Worker: Login non riuscito con password vuota su %s (RET: 0x%X). Proseguo comunque...", ip, loginRes[6])
	} else {
		log.Printf("TCP Worker: Login riuscito su %s!", ip)
	}

	// --- 1. Scaricamento Profili Staff (Command 0x72) ---
	staffMode := byte(0x01) // 0x01 per iniziare, 0x00 per le pagine successive
	for {
		staffReqData := []byte{staffMode}
		staffPacket := BuildAnvizPacket(deviceID, 0x72, staffReqData)

		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(staffPacket); err != nil {
			log.Printf("TCP Worker: Errore durante tcp write (staff): %v", err)
			break
		}
		
		staffRes := readFullAnvizPacket(conn)
		if staffRes != nil && staffRes[6] == 0x00 { // 0x00 Success
			count := int(staffRes[9])
			parseAnvizResponse(staffRes, ip)

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
	mode := byte(0x02)
	limit := byte(0x19) // 25 records alla volta (max supportato da molti vecchi firmware in un colpo)

	for {
		reqData := []byte{mode, limit}
		packet := BuildAnvizPacket(deviceID, 0x40, reqData)

		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(packet); err != nil {
			log.Printf("TCP Worker: Errore durante tcp write (dati): %v", err)
			break
		}

		recordRes := readFullAnvizPacket(conn)
		if recordRes != nil && recordRes[6] == 0x00 { // 0x00 Success
			count := int(recordRes[9])
			parseAnvizResponse(recordRes, ip)

			// Se il server ci ha restituito meno di 25 record, significa che li abbiamo esauriti tutti
			if count < 25 {
				break
			}
			// Per i pacchetti successivi la mode deve diventare 0 (Next Page)
			mode = 0x00
		} else {
			break
		}
	}

	// --- 3. Clear New Records (Command 0x4E) ---
	// Se abbiamo scaricato con successo (arrivando alla fine del loop sopra),
	// inviamo il comando per resettare il puntatore "nuovi record" sull'hardware.
	if mode == 0x00 { // Significa che eravamo nel loop di "scaricamento pagine successive"
		log.Printf("TCP Worker: Invio comando CLEAR per resettare puntatore nuovi record su %s", ip)
		clearPacket := BuildAnvizPacket(deviceID, AnvizCommandClear, nil)
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(clearPacket); err != nil {
			log.Printf("TCP Worker: Errore durante tcp write (clear): %v", err)
		} else {
			clearRes := readFullAnvizPacket(conn)
			if clearRes != nil && clearRes[6] == 0x00 {
				log.Printf("TCP Worker: Puntatore nuovi record resettato con successo su %s", ip)
			} else {
				if clearRes != nil {
					log.Printf("TCP Worker: Dispositivo ha rifiutato il comando clear su %s (RET: 0x%X)", ip, clearRes[6])
				} else {
					log.Printf("TCP Worker: Timeout comando clear su %s", ip)
				}
			}
		}
	} else {
		log.Printf("TCP Worker: Nessun nuovo record scaricato o loop interrotto, salto comando CLEAR su %s", ip)
	}

	// Opzionalmente si dovrebbe inviare COMMAND CLEAR (TC_C es. 0x4E) dopo lettura corretta.
	log.Println("TCP Worker: Sincronizzazione conclusa con l'Anviz.")
	return nil
}
