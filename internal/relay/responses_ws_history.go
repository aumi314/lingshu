package relay

// WebSocket 会话的历史缓存：记住每次响应"发出去的输入"和"回来的输出"，
// 客户端下一轮用 previous_response_id 指回来时，网关把历史展开进新请求里。
// 缓存只在进程内，不落库：所有连接共用一份，总量有上限，条目不够新就被淘汰。

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"
)

const (
	// wsHistoryMaxBytes 历史缓存的容量上限：256MiB。
	// 里面算的是数据本身，再加上一份粗略的簿记开销（每条记录、每段共享字节各算一点），
	// 所以实际占用比这个数略高一点，但不会高出一个数量级。
	wsHistoryMaxBytes = 256 << 20
	// wsHistoryMaxEntryBytes 单条记录的上限。超了的记录整条不存：
	// 宁可让下次引用报 previous_response_not_found，也不能悄悄砍掉一段历史，
	// 不然模型看到的上下文和客户端以为的不一样。
	wsHistoryMaxEntryBytes = 64 << 20
	// wsHistoryKeepPerConn 每条连接保留最近几个响应 id。
	wsHistoryKeepPerConn = 2
	// 簿记开销：map、切片、哈希键大概占多少。
	wsHistoryEntryOverhead = 256
	wsHistoryBlobOverhead  = 64
)

// wsBlob 是一段被多条记录共用的字节。同一段历史被反复引用时只存一份，容量按这一份算。
type wsBlob struct {
	data   []byte
	refs   int  // 有多少条记录引用它
	shared bool // 是不是在池子里（可以共享）
}

// wsHistoryEntry 是一次响应的历史：请求里发出去的输入，加上响应回来的输出。
type wsHistoryEntry struct {
	userID int64
	input  []*wsBlob
	output []*wsBlob
	used   time.Time
	pins   int // 有多少条连接把它钉住
}

// wsHistoryStore 是所有连接共享的历史缓存。
// 淘汰顺序：先淘汰没被任何连接钉住的记录（最久不用的先走），全都钉住时也照样淘汰最旧的一条，
// 保证内存总量不会超过上限，哪怕所有记录都被钉住。
type wsHistoryStore struct {
	mu    sync.Mutex
	items map[string]*wsHistoryEntry
	pool  map[[sha256.Size]byte]*wsBlob // 内容哈希 -> 共享的字节
	size  int                           // 已经用掉的容量（含簿记开销）
	max   int
}

func newWSHistoryStore(max int) *wsHistoryStore {
	if max <= 0 {
		max = wsHistoryMaxBytes
	}
	return &wsHistoryStore{
		items: map[string]*wsHistoryEntry{},
		pool:  map[[sha256.Size]byte]*wsBlob{},
		max:   max,
	}
}

// put 记下一次响应。记录属于 userID，别人拿同一个 id 也取不到。
// 整条超过上限就不存，下次引用会拿到 previous_response_not_found。
func (s *wsHistoryStore) put(userID int64, id string, input, output []json.RawMessage) {
	if id == "" {
		return
	}
	if wsHistoryBytes(input, output) > wsHistoryMaxEntryBytes {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry := &wsHistoryEntry{userID: userID, used: time.Now()}
	entry.input = s.internLocked(input)
	entry.output = s.internLocked(output)
	s.size += wsHistoryEntryOverhead
	if old, ok := s.items[id]; ok {
		s.releaseLocked(old)
	}
	s.items[id] = entry
	s.evictLocked()
}

// get 取一次响应的历史。记录不存在、不是这个用户的、或者已经被淘汰，都返回 false。
func (s *wsHistoryStore) get(userID int64, id string) (input, output []json.RawMessage, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.items[id]
	if !ok || entry.userID != userID {
		return nil, nil, false
	}
	entry.used = time.Now()
	return entry.inputItems(), entry.outputItems(), true
}

// pin 钉住这些 id：连接正在用它们，不让它们被优先淘汰。
func (s *wsHistoryStore) pin(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if entry, ok := s.items[id]; ok {
			entry.pins++
		}
	}
}

// remove 删掉这些记录。连接断了、或者这条 id 已经不是连接最近的那几个，就直接清掉，
// 别让没人引用的历史一直占着容量。
func (s *wsHistoryStore) remove(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		entry, ok := s.items[id]
		if !ok {
			continue
		}
		delete(s.items, id)
		s.releaseLocked(entry)
	}
}

// internLocked 把一段字节放进池子里，重复的内容共用一份。调用方持有锁。
// 池子的键是内容哈希，不是内容本身：哈希固定 32 字节，比整段内容省得多。
func (s *wsHistoryStore) internLocked(items []json.RawMessage) []*wsBlob {
	if len(items) == 0 {
		return nil
	}
	out := make([]*wsBlob, 0, len(items))
	for _, item := range items {
		key := sha256.Sum256(item)
		if blob, ok := s.pool[key]; ok {
			if bytes.Equal(blob.data, item) {
				blob.refs++
				out = append(out, blob)
				continue
			}
			// 哈希撞了（基本不可能）：不共享，单独存一份
			out = append(out, s.newBlob(item, false))
			continue
		}
		blob := s.newBlob(item, true)
		s.pool[key] = blob
		out = append(out, blob)
	}
	return out
}

// newBlob 存一段字节并记容量。调用方持有锁。
func (s *wsHistoryStore) newBlob(item json.RawMessage, shared bool) *wsBlob {
	blob := &wsBlob{data: append([]byte(nil), item...), refs: 1, shared: shared}
	s.size += len(blob.data) + wsHistoryBlobOverhead
	return blob
}

// releaseLocked 放掉一条记录引用的字节。调用方持有锁。
func (s *wsHistoryStore) releaseLocked(entry *wsHistoryEntry) {
	s.size -= wsHistoryEntryOverhead
	for _, blob := range entry.input {
		s.releaseBlobLocked(blob)
	}
	for _, blob := range entry.output {
		s.releaseBlobLocked(blob)
	}
}

// releaseBlobLocked 放掉一段共享字节。最后一个引用没了才真正删掉。
func (s *wsHistoryStore) releaseBlobLocked(blob *wsBlob) {
	blob.refs--
	if blob.refs > 0 {
		return
	}
	s.size -= len(blob.data) + wsHistoryBlobOverhead
	if blob.shared {
		delete(s.pool, sha256.Sum256(blob.data))
	}
}

// evictLocked 删掉多余的记录，把总量压回上限。调用方持有锁。
func (s *wsHistoryStore) evictLocked() {
	for s.size > s.max {
		victim := s.oldestLocked(func(e *wsHistoryEntry) bool { return e.pins == 0 })
		if victim == nil {
			// 全被钉住了也得淘汰，容量上限是硬的
			victim = s.oldestLocked(nil)
		}
		if victim == nil {
			return
		}
		for id, entry := range s.items {
			if entry == victim {
				delete(s.items, id)
				s.releaseLocked(victim)
				break
			}
		}
	}
}

// oldestLocked 找最久没用过的记录。match 为 nil 时不筛。
func (s *wsHistoryStore) oldestLocked(match func(*wsHistoryEntry) bool) *wsHistoryEntry {
	var oldest *wsHistoryEntry
	for _, entry := range s.items {
		if match != nil && !match(entry) {
			continue
		}
		if oldest == nil || entry.used.Before(oldest.used) {
			oldest = entry
		}
	}
	return oldest
}

// inputItems 把记录里的共享字节还原成消息列表。
func (e *wsHistoryEntry) inputItems() []json.RawMessage {
	return blobItems(e.input)
}

// outputItems 把记录里的共享字节还原成消息列表。
func (e *wsHistoryEntry) outputItems() []json.RawMessage {
	return blobItems(e.output)
}

func blobItems(blobs []*wsBlob) []json.RawMessage {
	if len(blobs) == 0 {
		return nil
	}
	out := make([]json.RawMessage, 0, len(blobs))
	for _, blob := range blobs {
		out = append(out, blob.data)
	}
	return out
}

// wsHistoryBytes 一条记录的数据量。
func wsHistoryBytes(input, output []json.RawMessage) int {
	total := 0
	for _, item := range input {
		total += len(item)
	}
	for _, item := range output {
		total += len(item)
	}
	return total
}
