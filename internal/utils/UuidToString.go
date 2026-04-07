package utils

import (
	"encoding/binary"
	"fmt"
)

// Helper function to convert UUID byte slice to string representation
func UuidToString(uuidBytes []byte) string {
	if len(uuidBytes) != 16 {
		return fmt.Sprintf("INVALID-UUID(%v)", uuidBytes)
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%04x%08x",
		binary.BigEndian.Uint32(uuidBytes[0:4]),
		binary.BigEndian.Uint16(uuidBytes[4:6]),
		binary.BigEndian.Uint16(uuidBytes[6:8]),
		binary.BigEndian.Uint16(uuidBytes[8:10]),
		binary.BigEndian.Uint16(uuidBytes[10:12]),
		binary.BigEndian.Uint32(uuidBytes[12:16]),
	)
}
