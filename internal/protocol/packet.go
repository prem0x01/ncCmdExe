// Package protocol defines the on-wire framing used between ncCmdExe peers.
//
// Frame layout (binary, big-endian):
//
//	┌──────────┬────────────────┬──────────────────┐
//	│ type (1) │ length (4)     │ payload (N)      │
//	└──────────┴────────────────┴──────────────────┘
package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

// ── Message types ─────────────────────────────────────────────────────────────

const (
	// Data / relay
	MsgText uint8 = 0x00 // raw relay bytes

	// Screen streaming
	MsgFrame uint8 = 0x01 // JPEG frame

	// Remote execution (interactive shell / exec)
	MsgData     uint8 = 0x10 // stdin/stdout/stderr bytes
	MsgResize   uint8 = 0x11 // terminal resize  [rows uint16 BE][cols uint16 BE]
	MsgCmdRun   uint8 = 0x12 // run a command    [NUL-terminated command string]
	MsgCmdExit  uint8 = 0x13 // command finished [exit-code uint32 BE]
	MsgCmdReady      uint8 = 0x14 // server ready for commands (sent after connect)
	MsgSysInfo       uint8 = 0x15 // OS/shell info [key=value\n...]
	MsgCmdInterrupt  uint8 = 0x16 // client → server: kill running command (Ctrl+C)
	MsgCwd           uint8 = 0x17 // server → client: current working directory

	// File transfer
	MsgFileHeader uint8 = 0x20 // start of file    [uint32 name-len][name][uint64 size BE]
	MsgFileData   uint8 = 0x21 // file chunk
	MsgFileDone   uint8 = 0x22 // file complete (zero-length payload)
	MsgFileAck    uint8 = 0x23 // receiver acknowledges file done
	MsgFileError  uint8 = 0x24 // transfer error   [error string]
)

// ── Framing constants ────────────────────────────────────────────────────────

// MaxPayload is the largest single payload accepted (64 MiB).
// Guards against malicious / corrupted length fields.
const MaxPayload = 64 << 20 // 64 MiB

const headerSize = 5 // 1 type + 4 length

// ── SendPacket / ReadPacket ──────────────────────────────────────────────────

// SendPacket writes one frame as a single Write call (minimises syscalls).
func SendPacket(w io.Writer, msgType uint8, data []byte) error {
	buf := make([]byte, headerSize+len(data))
	buf[0] = msgType
	binary.BigEndian.PutUint32(buf[1:], uint32(len(data)))
	copy(buf[headerSize:], data)
	_, err := w.Write(buf)
	return err
}

// ReadPacket reads exactly one frame from r and returns type + payload.
func ReadPacket(r io.Reader) (uint8, []byte, error) {
	var hdr [headerSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	msgType := hdr[0]
	length := binary.BigEndian.Uint32(hdr[1:])
	if length > MaxPayload {
		return 0, nil, fmt.Errorf("payload too large: %d (max %d)", length, MaxPayload)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return msgType, payload, nil
}

// ── Helpers for structured payloads ─────────────────────────────────────────

// EncodeResize encodes a terminal resize event.
func EncodeResize(rows, cols uint16) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint16(b[0:], rows)
	binary.BigEndian.PutUint16(b[2:], cols)
	return b
}

// DecodeResize decodes a MsgResize payload.
func DecodeResize(p []byte) (rows, cols uint16, err error) {
	if len(p) < 4 {
		return 0, 0, fmt.Errorf("resize payload too short")
	}
	return binary.BigEndian.Uint16(p[0:]), binary.BigEndian.Uint16(p[2:]), nil
}

// EncodeExitCode encodes a command exit code for MsgCmdExit.
func EncodeExitCode(code int) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(code))
	return b
}

// DecodeExitCode decodes a MsgCmdExit payload.
func DecodeExitCode(p []byte) int {
	if len(p) < 4 {
		return -1
	}
	return int(binary.BigEndian.Uint32(p))
}

// EncodeFileHeader encodes name + size for MsgFileHeader.
func EncodeFileHeader(name string, size uint64) []byte {
	nb := []byte(name)
	buf := make([]byte, 4+len(nb)+8)
	binary.BigEndian.PutUint32(buf[0:], uint32(len(nb)))
	copy(buf[4:], nb)
	binary.BigEndian.PutUint64(buf[4+len(nb):], size)
	return buf
}

// DecodeFileHeader decodes a MsgFileHeader payload.
func DecodeFileHeader(p []byte) (name string, size uint64, err error) {
	if len(p) < 4 {
		return "", 0, fmt.Errorf("file header too short")
	}
	nameLen := int(binary.BigEndian.Uint32(p[0:]))
	if len(p) < 4+nameLen+8 {
		return "", 0, fmt.Errorf("file header truncated")
	}
	name = string(p[4 : 4+nameLen])
	size = binary.BigEndian.Uint64(p[4+nameLen:])
	return name, size, nil
}
