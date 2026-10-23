// Package smf adapts Standard MIDI Files to the project model. Untrusted
// input is framed and bounded by this package before the gomidi decoder
// (whose known defects — SMPTE panics, truncated-header acceptance — are
// excluded by the preflight) ever sees it. Export is hand-encoded so the
// output is deterministic.
package smf

import (
	"encoding/binary"
	"fmt"

	"icetudio/internal/project"
)

// Input resource limits (docs/p1-project-model.md §3.4).
const (
	MaxFileSize     = 10 << 20
	MaxTrackEvents  = 100000
	MaxTotalEvents  = 1000000
	MaxRawEventSize = 16384
)

func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
func be16(b []byte) uint16 { return binary.BigEndian.Uint16(b) }

// framing is the strictly validated header view of an SMF file.
type framing struct {
	format   uint16
	ntrks    uint16
	division uint16
}

// validateFraming walks the chunk structure without decoding events. It
// rejects everything gomidi is known to mishandle: SMPTE divisions,
// malformed/truncated headers, mismatched track counts and trailing bytes.
func validateFraming(data []byte) (framing, error) {
	var f framing
	if len(data) > MaxFileSize {
		return f, fmt.Errorf("文件超过大小上限 %d 字节", MaxFileSize)
	}
	if len(data) < 14 || string(data[0:4]) != "MThd" {
		return f, fmt.Errorf("不是标准 MIDI 文件（缺少 MThd 头）")
	}
	if be32(data[4:8]) != 6 {
		return f, fmt.Errorf("MThd 块长度必须为 6，收到 %d；疑似截断或非标准文件", be32(data[4:8]))
	}
	f.format = be16(data[8:10])
	f.ntrks = be16(data[10:12])
	f.division = be16(data[12:14])
	if f.format > 1 {
		return f, fmt.Errorf("不支持 SMF format %d（仅支持 0 和 1）", f.format)
	}
	if f.ntrks < 1 {
		return f, fmt.Errorf("轨数不能为 0")
	}
	if f.ntrks > project.MaxTracks {
		return f, fmt.Errorf("轨数 %d 超过上限 %d", f.ntrks, project.MaxTracks)
	}
	if f.division&0x8000 != 0 {
		return f, fmt.Errorf("不支持 SMPTE 时间格式（division=0x%04x）；仅支持 PPQ", f.division)
	}
	if f.division < 1 || f.division > project.MaxPPQ {
		return f, fmt.Errorf("PPQ %d 超出支持范围 1–%d", f.division, project.MaxPPQ)
	}
	mtrks := 0
	pos := 14
	for pos < len(data) {
		if pos+8 > len(data) {
			return f, fmt.Errorf("文件尾部存在不完整的块头（截断）")
		}
		id := string(data[pos : pos+4])
		size := int(be32(data[pos+4 : pos+8]))
		if size < 0 || pos+8+size > len(data) {
			return f, fmt.Errorf("块 %q 声明长度 %d 超出文件剩余字节（截断）", id, size)
		}
		if id == "MTrk" {
			mtrks++
		}
		pos += 8 + size
	}
	if mtrks != int(f.ntrks) {
		return f, fmt.Errorf("实际 MTrk 块数 %d 与头声明 %d 不符", mtrks, f.ntrks)
	}
	return f, nil
}

func absTickCheck(t int64) error {
	if t > int64(1)<<31-1 {
		return fmt.Errorf("绝对 tick %d 超过支持上限 2^31-1", t)
	}
	return nil
}
