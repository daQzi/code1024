package runner

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/require"
)

func TestCredentialTypedWirePreservesOmittedAndExplicitNull(t *testing.T) {
	const meta = `export const meta={apiVersion:1,key:"credential-wire",name:"Credential wire",version:"1.0.0",author:{name:"Test"},relay:[{incoming_path:"/v1/chat/completions",upstream_path:"/chat"}]};`
	for _, explicitNull := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted", true: "explicit-null"}[explicitNull], func(t *testing.T) {
			member := ""
			if explicitNull {
				member = ",session:null"
			}
			source := meta + `export function login(){return {type:"pending"` + member + `};} export function scheduled(){return {next:120` + map[bool]string{false: "", true: ",credential:null"}[explicitNull] + `};}`
			plugin, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
			require.NoError(t, err)
			raw, err := callTyped(context.Background(), plugin, "CallLogin", []json.RawMessage{json.RawMessage(`"https://example.invalid"`), json.RawMessage(`{}`), json.RawMessage(`null`), json.RawMessage(`null`)})
			require.NoError(t, err)
			var steps []jsplugin.LoginStep
			require.NoError(t, json.Unmarshal(raw, &steps))
			require.Len(t, steps, 1)
			if explicitNull {
				require.Equal(t, "null", string(steps[0].Session), "explicit null clears an existing opaque session")
			} else {
				require.Nil(t, steps[0].Session, "omission must keep the opaque login session")
			}
			raw, err = callTyped(context.Background(), plugin, "CallScheduled", []json.RawMessage{json.RawMessage(`{"Credential":{}}`)})
			require.NoError(t, err)
			var scheduled []jsplugin.ScheduledResult
			require.NoError(t, json.Unmarshal(raw, &scheduled))
			require.Len(t, scheduled, 1)
			if explicitNull {
				require.Equal(t, "null", string(scheduled[0].Credential))
			} else {
				require.Nil(t, scheduled[0].Credential, "balance-only maintenance must never erase login data")
			}
		})
	}
}
