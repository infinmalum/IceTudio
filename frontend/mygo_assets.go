// Package pianoweb embeds the existing GridSound subset and isolated MyGo
// piano page. It does not embed the ordinary React application or FL images.
package pianoweb

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed all:mygo-web all:public/vendor/gridsound
var files embed.FS

type pianoFS struct{}

func (pianoFS) Open(name string) (fs.File, error) {
	if strings.HasPrefix(name, "vendor/gridsound/") {
		name = "public/" + name
	}
	return files.Open(name)
}

var MyGoAssets fs.FS = pianoFS{}
