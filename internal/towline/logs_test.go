package towline

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStripDockerLogHeaders(t *testing.T) {
	// Build a Docker multiplexed log frame:
	// Header: [stream_type=1 (stdout)][0][0][0][size=13 (big-endian)][payload="Hello, World\n"]
	payload := []byte("Hello, World\n")
	frame := make([]byte, 8+len(payload))
	frame[0] = 1 // stdout
	frame[1] = 0
	frame[2] = 0
	frame[3] = 0
	// Size in big-endian
	size := len(payload)
	frame[4] = byte(size >> 24)
	frame[5] = byte(size >> 16)
	frame[6] = byte(size >> 8)
	frame[7] = byte(size)
	copy(frame[8:], payload)

	result := stripDockerLogHeaders(frame)
	assert.Equal(t, "Hello, World\n", result)
}

func TestStripDockerLogHeaders_MultipleFrames(t *testing.T) {
	// Two frames: stdout "line1\n" and stderr "error\n"
	buildFrame := func(streamType byte, payload string) []byte {
		data := []byte(payload)
		frame := make([]byte, 8+len(data))
		frame[0] = streamType
		size := len(data)
		frame[4] = byte(size >> 24)
		frame[5] = byte(size >> 16)
		frame[6] = byte(size >> 8)
		frame[7] = byte(size)
		copy(frame[8:], data)
		return frame
	}

	var combined []byte
	combined = append(combined, buildFrame(1, "line1\n")...)
	combined = append(combined, buildFrame(2, "error\n")...)

	result := stripDockerLogHeaders(combined)
	assert.Equal(t, "line1\nerror\n", result)
}

func TestStripDockerLogHeaders_EmptyInput(t *testing.T) {
	result := stripDockerLogHeaders([]byte{})
	assert.Equal(t, "", result)
}

func TestStripDockerLogHeaders_ShortInput(t *testing.T) {
	// Less than 8 bytes: treated as raw data
	result := stripDockerLogHeaders([]byte("short"))
	assert.Equal(t, "short", result)
}

func TestFilterLines(t *testing.T) {
	text := "INFO starting server\nERROR connection failed\nINFO request handled\nWARN slow query\n"

	result := filterLines(text, "ERROR")
	assert.Equal(t, "ERROR connection failed\n", result)

	result = filterLines(text, "INFO")
	assert.Equal(t, "INFO starting server\nINFO request handled\n", result)

	result = filterLines(text, "NOTFOUND")
	assert.Equal(t, "", result)
}
