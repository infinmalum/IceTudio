// icetudio-mygo is the explicit preview/disposable native regression entry.
// The root icetudio binary launches the normal MyGo desktop.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"icetudio/internal/desktop"
	"icetudio/internal/host"
)

func main() {
	preview := flag.Bool("preview", false, "只读视觉样例（默认），不启动宿主或访问用户数据")
	data := flag.String("data", "", "现有数据目录的绝对路径（启用已接通的音乐编辑，使用正常宿主启动/恢复）")
	smoke := flag.String("smoke-dir", "", "保存 M1 原生/WebView 验证截图和结果后退出")
	smokeEdit := flag.Bool("smoke-edit", false, "在临时数据上验证原生工程/属性/时间图/MIDI 及卷帘变换/撤销/恢复（必须配合 --data 和 --smoke-dir）")
	flag.Parse()
	if *smokeEdit && (*data == "" || *smoke == "") {
		log.Fatal("--smoke-edit 必须指定 --data 和 --smoke-dir；会修改测试工程的编辑历史")
	}
	if flag.NArg() != 0 || (*preview && *data != "") {
		log.Fatal("不接受位置参数；--preview 与 --data 不能同时使用")
	}
	// Direct launch (including a file-manager double click) opens the safe
	// preview. Access to an existing host directory remains explicit.
	if *data == "" {
		if err := desktop.Run(nil, desktop.Options{SmokeDir: *smoke}); err != nil {
			log.Fatal(err)
		}
		return
	}
	if !filepath.IsAbs(*data) {
		log.Fatal("--data 必须是绝对路径")
	}
	if err := os.Setenv("ICETUDIO_DATA_DIR", *data); err != nil {
		log.Fatal(err)
	}
	h, err := host.Start()
	if err != nil {
		log.Fatal(err)
	}
	if h.InitError != nil {
		h.Close()
		log.Fatal(h.InitError)
	}
	err = desktop.Run(h.Service, desktop.Options{StateDir: *data, SmokeDir: *smoke, SmokeEdit: *smokeEdit})
	h.Close()
	if err != nil {
		log.Fatal(err)
	}
}
