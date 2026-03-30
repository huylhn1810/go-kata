package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"
)

type LineReader interface {
	ReadNDJSON(ctx context.Context, r io.Reader, handle func([]byte) error) error
}

type lineReader struct {
	pool   *sync.Pool
	reader *bufio.Reader
}

func NewReader() LineReader {
	return &lineReader{
		pool: &sync.Pool{
			New: func() any {
				return make([]byte, 0, 1024)
			},
		},
		reader: nil,
	}
}

func (lr *lineReader) ReadNDJSON(ctx context.Context, r io.Reader, handle func([]byte) error) error {
	count := 0
	isNotReachEOF := true
	lr.reader = bufio.NewReader(r)

	for isNotReachEOF {
		select {
		case <-ctx.Done():
			return fmt.Errorf("Canceled: %w", ctx.Err())
		default:
		}

		line := lr.pool.Get().([]byte)
		line = line[:0]
		count++

		for {
			dataRead, err := lr.reader.ReadSlice('\n')

			if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
				lr.pool.Put(line)
				return fmt.Errorf("Line: %d Error: %w", count, err)
			}

			line = append(line, dataRead...)

			if err == nil || err == io.EOF {
				// Remove trailing newline(s) only when the end of line is actually reached
				if n := len(line); n > 0 {
					if line[n-1] == '\n' {
						line = line[:n-1]
						n--
					}
					if n > 0 && line[n-1] == '\r' {
						line = line[:n-1]
					}
				}

				if err == io.EOF {
					isNotReachEOF = false
				}
				break
			}
		}

		// Avoid processing a phantom empty line if the file ends with a trailing newline
		if len(line) == 0 && !isNotReachEOF {
			lr.pool.Put(line)
			break
		}

		err := handle(line)
		if err != nil {
			lr.pool.Put(line)
			return fmt.Errorf("Line: %d Error: %w", count, err)
		}

		lr.pool.Put(line)
	}

	return nil
}
