// SPDX-License-Identifier: AGPL-3.0-or-later
package suite

import (
	"html/template"
	"net/http"
)

var discussionPage = template.Must(template.New("return").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark"><title>Keep discussion link</title><link rel="stylesheet" href="{{.CSS}}"></head><body><main class="comfy-auth"><h1>Keep this discussion link?</h1><p>The discussion has its own audience in Witmoot. Saving its link does not change who can read the source item.</p><p><a href="{{.Thread}}" rel="noreferrer">Open discussion in Witmoot</a></p><form method="post" action="{{.Action}}"><input type="hidden" name="{{.CSRFKey}}" value="{{.CSRF}}"><input type="hidden" name="url" value="{{.Thread}}"><input type="hidden" name="discussion_state" value="{{.State}}"><button type="submit">Keep discussion link</button></form><p><a href="{{.Source}}">Return without saving</a></p></main></body></html>`))

func RenderDiscussion(w http.ResponseWriter, action, source, thread, state, csrfKey, csrf, css string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = discussionPage.Execute(w, struct{ Action, Source, Thread, State, CSRFKey, CSRF, CSS string }{action, source, thread, state, csrfKey, csrf, css})
}
