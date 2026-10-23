package main

import "github.com/wailsapp/wails/v3/pkg/application"

// Picking a folder does not grant access. The UI explains and confirms that
// separate permission before changing the conversation's local policy.
func (s *StatusService) PickFolder() (string, error) {
	return application.Get().Dialog.OpenFile().SetTitle("选择要交给 IceTudio 的文件夹").CanChooseFiles(false).CanChooseDirectories(true).PromptForSingleSelection()
}
