package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
)

// 对话留存（docs/decisions.md「对话留存」）：客户端每轮都重发完整历史，按请求原样存体积会被重复内容撑爆。
// 请求里的消息逐条、system / instructions / tools 整块按内容哈希去重存成块，每次调用只存骨架。

// splitFields 是拆成内容块的请求字段：数组逐条拆，其余（字符串、对象）够长就整块拆。
var splitFields = []string{"messages", "input", "system", "instructions", "tools"}

const (
	blobListKey  = "$lingshu_blobs" // 骨架里代替数组：按顺序的块哈希
	blobOneKey   = "$lingshu_blob"  // 骨架里代替整块内容
	minWholeBlob = 512              // 短于这个的字符串、对象留在骨架里，不值得单独成块
	wholeBodyKey = "$lingshu_body"  // 请求体不是 JSON 对象时，整个请求体作为一块
)

// splitBody 把请求体拆成骨架和内容块（哈希 → 原文）。
func splitBody(body []byte) ([]byte, map[[32]byte][]byte) {
	blobs := map[[32]byte][]byte{}
	put := func(raw []byte) string {
		h := sha256.Sum256(raw)
		blobs[h] = raw
		return hex.EncodeToString(h[:])
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		skeleton, _ := json.Marshal(map[string]string{wholeBodyKey: put(body)})
		return skeleton, blobs
	}
	for _, name := range splitFields {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) == nil && items != nil {
			hashes := make([]string, len(items))
			for i, item := range items {
				hashes[i] = put(item)
			}
			fields[name], _ = json.Marshal(map[string][]string{blobListKey: hashes})
		} else if len(raw) >= minWholeBlob {
			fields[name], _ = json.Marshal(map[string]string{blobOneKey: put(raw)})
		}
	}
	skeleton, _ := json.Marshal(fields)
	return skeleton, blobs
}

// joinBody 按骨架和内容块还原请求体，和原请求语义一致（顶层字段顺序可能不同）。
func joinBody(skeleton []byte, blob func(hash string) ([]byte, error)) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(skeleton, &fields); err != nil {
		return nil, err
	}
	if raw, ok := fields[wholeBodyKey]; ok && len(fields) == 1 {
		var h string
		json.Unmarshal(raw, &h)
		return blob(h)
	}
	for _, name := range splitFields {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		var ref struct {
			List *[]string `json:"$lingshu_blobs"`
			One  *string   `json:"$lingshu_blob"`
		}
		if json.Unmarshal(raw, &ref) != nil {
			continue
		}
		switch {
		case ref.List != nil:
			items := make([]json.RawMessage, len(*ref.List))
			for i, h := range *ref.List {
				b, err := blob(h)
				if err != nil {
					return nil, err
				}
				items[i] = b
			}
			fields[name], _ = json.Marshal(items)
		case ref.One != nil:
			b, err := blob(*ref.One)
			if err != nil {
				return nil, err
			}
			fields[name] = b
		}
	}
	return json.Marshal(fields)
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

func gunzipBytes(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// Capture 是一次调用要留存的内容。
type Capture struct {
	RequestID         string
	Session           string
	Request           []byte // 客户端原始请求体
	Response          []byte // 客户端收到的回复（可能已截断）
	ResponseTruncated bool
}

// SaveCapture 存一次调用的对话：已存在的内容块不再压缩、不再写。调用日志要先写好（外键）。
func (s *Store) SaveCapture(ctx context.Context, c Capture) error {
	skeleton, blobs := splitBody(c.Request)
	hashes := make([][]byte, 0, len(blobs))
	for h := range blobs {
		hashes = append(hashes, h[:])
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT hash FROM conversation_blobs WHERE hash = ANY($1)`, hashes)
		if err != nil {
			return err
		}
		have, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
		if err != nil {
			return err
		}
		for _, h := range have {
			var k [32]byte
			copy(k[:], h)
			delete(blobs, k)
		}
		var newHashes, datas [][]byte
		var sizes []int32
		for h, raw := range blobs {
			newHashes = append(newHashes, append([]byte(nil), h[:]...))
			datas = append(datas, gzipBytes(raw))
			sizes = append(sizes, int32(len(raw)))
		}
		if len(newHashes) > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO conversation_blobs (hash, data, size)
				SELECT * FROM unnest($1::bytea[], $2::bytea[], $3::int[]) ON CONFLICT DO NOTHING`, newHashes, datas, sizes); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO request_captures (request_id, session, skeleton, request_bytes, response, response_bytes, response_truncated)
			VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (request_id) DO NOTHING`,
			c.RequestID, c.Session, gzipBytes(skeleton), len(c.Request), gzipBytes(c.Response), len(c.Response), c.ResponseTruncated)
		return err
	})
}

// CaptureView 是还原后的一次调用的对话，给管理端看。
type CaptureView struct {
	Session           string `json:"session"`
	Protocol          string `json:"protocol"`
	Request           []byte `json:"-"`
	Response          []byte `json:"-"`
	ResponseTruncated bool   `json:"response_truncated"`
}

// LoadCapture 按调用日志 id 还原这次调用的对话；没有留存返回 ErrNotFound。
func (s *Store) LoadCapture(ctx context.Context, logID int64) (*CaptureView, error) {
	var v CaptureView
	var skeleton, response []byte
	err := s.Pool.QueryRow(ctx, `SELECT c.session, l.protocol, c.skeleton, c.response, c.response_truncated
		FROM request_captures c JOIN request_logs l ON l.request_id = c.request_id WHERE l.id = $1`, logID).
		Scan(&v.Session, &v.Protocol, &skeleton, &response, &v.ResponseTruncated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if skeleton, err = gunzipBytes(skeleton); err != nil {
		return nil, err
	}
	if v.Response, err = gunzipBytes(response); err != nil {
		return nil, err
	}
	// 先收集骨架里引用的所有块，一次查回来（长对话有几百条消息）
	var refs [][]byte
	joinBody(skeleton, func(h string) ([]byte, error) {
		if key, err := hex.DecodeString(h); err == nil {
			refs = append(refs, key)
		}
		return []byte("null"), nil
	})
	rows, err := s.Pool.Query(ctx, `SELECT hash, data FROM conversation_blobs WHERE hash = ANY($1)`, refs)
	if err != nil {
		return nil, err
	}
	found := map[string][]byte{}
	for rows.Next() {
		var h, data []byte
		if err := rows.Scan(&h, &data); err != nil {
			rows.Close()
			return nil, err
		}
		found[hex.EncodeToString(h)] = data
	}
	rows.Close()
	v.Request, err = joinBody(skeleton, func(h string) ([]byte, error) {
		data, ok := found[h]
		if !ok {
			return nil, fmt.Errorf("内容块 %s 缺失", h[:min(12, len(h))])
		}
		return gunzipBytes(data)
	})
	return &v, err
}
