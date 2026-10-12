package application

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Files in the conversation: what an agent writes during a task is attached, as
// references (path, id, version, never content), to the message the agent posts
// in the chat that started the request; what the user attaches reaches the
// agents as paths they read with fs.read.

// FileResolver turns a drive path into a reference (installed by the server
// when the drive is configured).
type FileResolver interface {
	ResolveFile(ctx context.Context, org, path string) (domain.FileRef, bool)
}

// SetFileResolver installs the drive path resolver (nil removes it).
func (o *Orchestrator) SetFileResolver(r FileResolver) { o.files.res = r }

const (
	maxTaskFiles   = 8 // files shown per task message
	maxAttachments = 6 // files per user message
	maxFileTracked = 2000
)

type chatFiles struct {
	res FileResolver
	mu  sync.Mutex
	by  map[string][]string // task id -> written paths (in order, unique)
}

// NoteTaskFile records that an agent wrote or moved a file while running a task.
func (o *Orchestrator) NoteTaskFile(taskID, action, p string) {
	if taskID == "" || p == "" || (action != "write" && action != "move") {
		return
	}
	f := &o.files
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.by == nil {
		f.by = map[string][]string{}
	}
	if len(f.by) >= maxFileTracked {
		f.by = map[string][]string{} // stale entries of tasks that never completed
	}
	for _, x := range f.by[taskID] {
		if x == p {
			return
		}
	}
	f.by[taskID] = append(f.by[taskID], p)
}

func (o *Orchestrator) takeTaskFiles(taskID string) []string {
	f := &o.files
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.by[taskID]
	delete(f.by, taskID)
	return v
}

// resolveFiles maps paths to references, dropping the ones that do not resolve.
func (o *Orchestrator) resolveFiles(ctx context.Context, paths []string, action string) []domain.FileRef {
	if o.files.res == nil {
		return nil
	}
	var out []domain.FileRef
	seen := map[string]bool{}
	for _, p := range paths {
		p = path.Clean("/" + strings.TrimSpace(p))
		if seen[p] {
			continue
		}
		seen[p] = true
		if ref, ok := o.files.res.ResolveFile(ctx, o.org(ctx), p); ok {
			ref.Action = action
			out = append(out, ref)
		}
	}
	return out
}

// chatTaskFiles posts, as the agent, a message with the files the task wrote.
// Only requests born in a chat; tasks that wrote nothing say nothing.
func (o *Orchestrator) chatTaskFiles(ctx context.Context, rs *run, t domain.Task, agent domain.Agent) {
	paths := o.takeTaskFiles(t.ID)
	l := rs.chat
	if l == nil || len(paths) == 0 {
		return
	}
	if len(paths) > maxTaskFiles {
		paths = paths[:maxTaskFiles]
	}
	refs := o.resolveFiles(ctx, paths, "write")
	if len(refs) == 0 {
		return
	}
	reply := l.replyTo
	o.chatStore(ctx, domain.Message{ID: newID(), ConversationID: l.conv, From: agent.ID, To: "user", Kind: domain.MsgChat,
		Text: fmt.Sprintf(chatText(l.loc, "task_files"), truncate(strings.TrimRight(t.Title, ". "), 120)),
		TS:   time.Now().UTC(), TurnID: l.turnID, ReplyTo: &reply, Files: refs}, rs.req.ID)
}

type attachKey struct{}

// WithAttachments carries the drive paths the user attached to a chat message.
func WithAttachments(ctx context.Context, paths []string) context.Context {
	return context.WithValue(ctx, attachKey{}, paths)
}

func attachmentsFrom(ctx context.Context) []string {
	p, _ := ctx.Value(attachKey{}).([]string)
	return p
}

// attachmentNote is the delimited block appended to the text the agents see.
func attachmentNote(loc string, refs []domain.FileRef) string {
	if len(refs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[attachments]\n" + chatText(loc, "attach_note"))
	for _, r := range refs {
		fmt.Fprintf(&b, "\n- %s", strings.NewReplacer("\n", " ", "\r", " ", "[", "(", "]", ")").Replace(r.Path))
	}
	b.WriteString("\n[/attachments]")
	return b.String()
}
