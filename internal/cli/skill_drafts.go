package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"icetudio/internal/ipc"
	"icetudio/internal/skills"
	"icetudio/internal/storage"
	"io"
	"os"
	"strconv"
	"strings"
)

func cmdSkillDrafts(client *ipc.Client, args []string) int {
	if len(args) == 0 {
		return usage("skill-drafts generate <conversationID> <需求> [--source <Skill名称>] [--evidence <runID,runID>] | list | show <id> | edit <id> <proposal.json> --revision N | apply|discard|withdraw <id> --revision N")
	}
	if args[0] == "generate" {
		p, err := parseCLI(args[1:])
		if err != nil || len(p.positional) != 2 {
			return usage("skill-drafts generate <conversationID> <需求> [--source <名称>] [--evidence <runID,runID>]")
		}
		if err = p.allowValues("source", "evidence"); err != nil {
			return usage(err.Error())
		}
		var r storage.Run
		if err = client.Call("skillDrafts.start", map[string]any{"sessionId": p.arg(0), "source": p.value("source"), "request": p.arg(1), "evidenceIds": draftEvidenceIDs(p.value("evidence"))}, &r); err != nil {
			return fail("生成Skill草案", err)
		}
		fmt.Printf("Skill草案任务 %s；尚未采用。\n", r.ID)
		return 0
	}
	var drafts []storage.SkillDraft
	if err := client.Call("skillDrafts.list", nil, &drafts); err != nil {
		return fail("Skill草案", err)
	}
	if args[0] == "list" {
		rows := [][]string{{"ID", "状态", "草案修订", "源Skill", "说明"}}
		for _, d := range drafts {
			rows = append(rows, []string{d.ID, d.State, strconv.Itoa(d.Revision), d.Source.Name, d.Proposal.Summary})
		}
		printTable(rows)
		return 0
	}
	p, err := parseCLI(args[1:])
	if err != nil || p.arg(0) == "" {
		return usage("请提供草案ID")
	}
	if err = p.allowValues("revision"); err != nil {
		return usage(err.Error())
	}
	var d storage.SkillDraft
	for _, item := range drafts {
		if item.ID == p.arg(0) {
			d = item
		}
	}
	if d.ID == "" {
		return usage("Skill草案不存在")
	}
	if args[0] == "show" {
		raw, _ := json.MarshalIndent(d, "", "  ")
		fmt.Println(string(raw))
		fmt.Println("采用会发布独立副本；显式引用不切换，选择全部Skills的配置会看到新副本。检查私人信息和依赖；未执行场景验收。")
		return 0
	}
	revision, err := strconv.Atoi(p.value("revision"))
	if err != nil || revision < 1 {
		return usage("需要 --revision N 指定已查看的草案修订")
	}
	params := map[string]any{"id": d.ID, "revision": revision}
	switch args[0] {
	case "edit":
		raw, err := os.ReadFile(p.arg(1))
		if err != nil {
			return fail("读取Skill草案", err)
		}
		if len(raw) > 512*1024 {
			return usage("草案超过512KiB")
		}
		var proposal storage.SkillProposal
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&proposal); err != nil {
			return fail("解析Skill草案", err)
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return usage("JSON后有额外内容")
		}
		params["proposal"] = proposal
		var saved storage.SkillDraft
		if err = client.Call("skillDrafts.edit", params, &saved); err != nil {
			return fail("保存Skill草案", err)
		}
		fmt.Printf("已保存草案v%d，尚未采用。\n", saved.Revision)
	case "apply":
		var copy skills.Snapshot
		if err = client.Call("skillDrafts.apply", params, &copy); err != nil {
			return fail("采用Skill草案", err)
		}
		fmt.Printf("已发布独立Skill %s（%s）；没有切换显式引用。\n", copy.Name, copy.SHA256)
	case "withdraw":
		if err = client.Call("skillDrafts.withdraw", params, nil); err != nil {
			return fail("撤销Skill副本", err)
		}
		fmt.Println("已撤销Skill副本，在途快照保留")
	case "discard":
		if err = client.Call("skillDrafts.discard", params, nil); err != nil {
			return fail("丢弃Skill草案", err)
		}
		fmt.Println("已丢弃草案")
	default:
		return usage("未知Skill草案子命令")
	}
	return 0
}

// draftEvidenceIDs splits the --evidence flag into run IDs; empty input means
// no explicit evidence was given.
func draftEvidenceIDs(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	out := strings.Split(value, ",")
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	return out
}
