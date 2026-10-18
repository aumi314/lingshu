package usage

import (
	"bufio"
	"bytes"
	"io"
)

// Event 是一个 SSE 事件：Raw 是原样的字节（含结尾空行），Data 是拼接后的 data 内容。
type Event struct {
	Raw  []byte
	Data []byte
}

// SSEReader 按事件读取 SSE 流，保留原始字节以便原样转发。
// 兼容 "data:" 后有无空格两种写法，兼容 \r\n 换行。
type SSEReader struct {
	r *bufio.Reader
}

func NewSSEReader(r io.Reader) *SSEReader {
	return &SSEReader{r: bufio.NewReaderSize(r, 64*1024)}
}

// Next 返回下一个事件；流结束时返回 io.EOF（结尾没有空行的最后一个事件也会先返回）。
func (s *SSEReader) Next() (*Event, error) {
	var raw, data []byte
	hasData := false
	for {
		line, err := s.r.ReadBytes('\n')
		raw = append(raw, line...)
		trimmed := bytes.TrimRight(line, "\r\n")

		if len(trimmed) == 0 && len(line) > 0 {
			if len(raw) == len(line) {
				// 事件之间多余的空行，丢掉继续读
				raw = raw[:0]
				if err != nil {
					return nil, err
				}
				continue
			}
			return &Event{Raw: raw, Data: data}, nil
		}
		if after, ok := bytes.CutPrefix(trimmed, []byte("data:")); ok {
			after = bytes.TrimPrefix(after, []byte(" "))
			if hasData {
				data = append(data, '\n')
			}
			data = append(data, after...)
			hasData = true
		}
		if err != nil {
			if len(bytes.TrimSpace(raw)) > 0 {
				return &Event{Raw: raw, Data: data}, nil
			}
			return nil, err
		}
	}
}
