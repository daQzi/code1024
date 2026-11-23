// SPDX-License-Identifier: AGPL-3.0-or-later
// media.text ordering and partial-read protection follow the pinned
// relay/requestplugin/{plan,attachments}.go. See ../NOTICE.md.
package mediahost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
)

type PreparedEffects struct {
	Effects   []requestpatch.Effect
	Media     []Change
	source    *Source
	rewritten []int
}

// PrepareEffects expands media effects before ordinary patches, because media
// indices refer to the view the hook saw. Commit marks rewrites only after all
// patches, protected-member checks and host memory admission have succeeded.
func (s *Source) PrepareEffects(ctx context.Context, effects []requestpatch.Effect) (*PreparedEffects, error) {
	prepared := &PreparedEffects{source: s}
	var mediaOps []requestpatch.Operation
	outputBytes := 0
	for _, effect := range effects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if effect.Type == requestpatch.EffectMediaReencode {
			if effect.Media < 0 || effect.Media >= len(s.items) {
				return nil, errors.New("media image index does not exist")
			}
			ops, change, err := encodeImage(ctx, s.items[effect.Media], effect, s.admitMemory)
			if err != nil {
				return nil, err
			}
			for _, op := range ops {
				outputBytes += len(op.Value) + len(op.Path) + 128
			}
			if outputBytes > MaxBody {
				return nil, errors.New("media output exceeds body limit")
			}
			mediaOps = append(mediaOps, ops...)
			if change != nil {
				change.Index = effect.Media
				change.From.Mime = recordMime(change.From.Mime)
				prepared.Media = append(prepared.Media, *change)
			}
			continue
		}
		if effect.Type != requestpatch.EffectMediaText {
			prepared.Effects = append(prepared.Effects, effect)
			continue
		}
		if effect.Media < 0 || effect.Media >= len(s.items) {
			return nil, errors.New("media text index does not exist")
		}
		value := s.items[effect.Media]
		ref := fmt.Sprintf("[media:%d]", effect.Media)
		if !rewritable(value) || s.marks[ref].Status == "truncated" {
			if s.marks == nil {
				s.marks = map[string]Record{}
			}
			s.marks[ref] = Record{Status: "unchecked", Reason: "not_rewritable"}
			continue
		}
		outputBytes += base64.StdEncoding.EncodedLen(len(effect.Text)) + len(value.mime) + len(value.path) + 128
		if !utf8.ValidString(effect.Text) || outputBytes > MaxBody {
			return nil, errors.New("media text output exceeds body limit")
		}
		payload := base64.StdEncoding.EncodeToString([]byte(effect.Text))
		if value.dataURL {
			payload = "data:" + value.mime + ";base64," + payload
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		mediaOps = append(mediaOps, requestpatch.Operation{Op: "replace", Path: value.path, Value: raw})
		prepared.rewritten = append(prepared.rewritten, effect.Media)
	}
	if len(mediaOps) > 0 {
		prepared.Effects = append([]requestpatch.Effect{{Type: requestpatch.EffectJSONPatch, Ops: mediaOps}}, prepared.Effects...)
	}
	return prepared, nil
}

func (p *PreparedEffects) Commit() {
	for _, index := range p.rewritten {
		p.source.mark(fmt.Sprintf("[media:%d]", index), "checked", "", true)
	}
}
