package desktop

import "github.com/egoist/mygo"

func (a *Workspace) readOnlyView() (*mygo.WebView, error) {
	view, err := a.win.NewWebView(mygo.WebViewOptions{URL: "/mygo-web/readonly.html", Page: mygo.PageOptions{Untrusted: true}})
	if err != nil {
		return nil, err
	}
	view.Page().SetWindowOpenHandler(func(request mygo.WindowOpenRequest) *mygo.WindowOptions {
		if request.UserInitiated && safeWebLink(request.URL) {
			a.update(func() { a.pendingURL = request.URL })
		}
		return nil
	})
	return view, nil
}
