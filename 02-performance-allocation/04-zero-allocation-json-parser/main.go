package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

type SensorParser interface {
	Parse(context.Context) (*SensorData, error)
}

type sensorParser struct {
	r    io.Reader
	dec  *json.Decoder
	pool *sync.Pool
}

func NewSensorParser(r io.Reader) SensorParser {
	return &sensorParser{
		r:   r,
		dec: json.NewDecoder(r),
		pool: &sync.Pool{
			New: func() any {
				return new(bytes.Buffer)
			},
		},
	}
}

type SensorData struct {
	sensorID string
	value    float64
}

func (sp *sensorParser) Parse(ctx context.Context) (*SensorData, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("ctx error")
		default:
		}

		data, err := sp.ParseObject()

		if err == io.EOF {
			return nil, err
		}

		if err != nil {
			err := sp.resync()

			// Failed to resync, propagate the error
			if err != nil {
				return nil, err
			}

			// Resync successful, try parsing the next object
			continue
		}

		return data, nil
	}
}

func (sp *sensorParser) ParseObject() (*SensorData, error) {
	data := &SensorData{}
	hasSensorID := false
	hasReadingValue := false
	depth := 0

	for {
		tok, err := sp.dec.Token()

		// Reached the end of the JSON stream
		if err == io.EOF {
			return nil, err
		}

		// Failed to retrieve the next JSON token
		if err != nil {
			return nil, fmt.Errorf("Error when parsing")
		}

		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				depth++
			case ']', '}':
				depth--
			}
		case string:
			switch v {
			case "sensor_id":
				tok, err = sp.dec.Token()
				if err == io.EOF {
					return nil, err
				}

				if err != nil {
					return nil, fmt.Errorf("Error when parsing")
				}

				if str, ok := tok.(string); ok {
					data.sensorID = str
					hasSensorID = true
					break
				} else {
					return nil, fmt.Errorf("sensorID must a string")
				}
			case "readings":
				tok, err = sp.dec.Token()
				if err == io.EOF {
					return nil, err
				}

				if err != nil {
					return nil, fmt.Errorf("Error when parsing")
				}

				if delim, ok := tok.(json.Delim); !ok || delim != '[' {
					return nil, fmt.Errorf("Error of 'reading JSON' input")
				}

				depth++

				tok, err = sp.dec.Token()
				if err != nil {
					return nil, err
				}

				if readValue, ok := tok.(float64); ok {
					data.value = readValue
					hasReadingValue = true
				} else {
					return nil, fmt.Errorf("First value of reading must a float64 type")
				}
			}
		}

		// Stop parsing this object early if both required fields are found
		// This avoids allocating memory or wasting CPU on the rest of the object
		if hasReadingValue && hasSensorID {
			return data, nil
		}

		if depth == 0 {
			break
		}
	}

	return nil, fmt.Errorf("not found data to parse")
}

func (sp *sensorParser) resync() error {
	buf := sp.pool.Get().(*bytes.Buffer)
	buf.Reset()

	// Drain all remaining unread bytes from the decoder's internal buffer
	_, err := buf.ReadFrom(sp.dec.Buffered())
	if err != nil {
		return fmt.Errorf("Error to get all bytes left in decode")
	}

	// Scan byte-by-byte to find the start of the next valid JSON object
	// Jump to next JSON
	var b [1]byte
	for {
		_, err := buf.Read(b[:])

		// Reached EOF while scanning; reset decoder with the remaining reader data
		if err == io.EOF {
			sp.dec = json.NewDecoder(io.MultiReader(sp.r))
			sp.pool.Put(buf)
			return nil
		}

		// Handle unexpected read errors during resync
		if err != nil {
			sp.pool.Put(buf)
			return fmt.Errorf("Error to resync")
		}

		// Found the start of a new JSON object '{'.
		// Recreate the decoder combining the matched byte, the buffered data, and the source reader
		if b[0] == '{' {
			sp.dec = json.NewDecoder(io.MultiReader(bytes.NewReader(b[:]), buf, sp.r))
			return nil
		}
	}
}
