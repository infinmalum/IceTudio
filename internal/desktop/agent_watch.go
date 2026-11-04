package desktop

import "time"

// Service events prompt a refresh but are not a complete call-state journal.
// A bounded, coalesced query also discovers approvals in hidden worker
// sessions and the last transition after an event/read race.
func (a *Workspace) watchAgent(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.update(func() {
				if a.agent.selected != "" && a.showAgent && !a.agent.loading {
					a.loadAgentHistory(false)
				}
			})
		}
	}
}
