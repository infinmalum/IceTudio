package main

import "github.com/egoist/mygo"

// Picking a folder does not grant access. The UI explains and confirms that
// separate permission before changing the conversation's local policy.
func (s *StatusService) PickFolder() (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "选择工作文件夹", Directory: true})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}
