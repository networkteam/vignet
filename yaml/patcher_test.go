package yaml_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/networkteam/vignet/yaml"
)

func TestPatcher(t *testing.T) {
	tests := []struct {
		name         string
		inputYAML    string
		fieldPath    string
		value        any
		createKeys   bool
		expectedYAML string
		expectErr    bool
	}{
		{
			name: "valid yaml with nested key and comment as annotation",
			inputYAML: `
foo: bar
spec:
  image:
    # some special comment
    tag: 0.1.0
`,
			fieldPath: "spec.image.tag",
			value:     "0.2.0",
			expectedYAML: `foo: bar
spec:
  image:
    # some special comment
    tag: 0.2.0
`,
		},
		{
			name: "yaml with non-leaf key",
			inputYAML: `
spec:
  image:
    tag:
      name: Foo
`,
			fieldPath: "spec.image.tag",
			value:     "0.2.0",
			expectErr: true,
		},
		{
			name:      "yaml without key",
			inputYAML: `spec:\n`,
			fieldPath: "spec.image.tag",
			value:     "0.2.0",
			expectErr: true,
		},
		{
			name: "yaml without key and create keys",
			inputYAML: `spec:
  image:
    name: my/image`,
			fieldPath:  "spec.image.tag",
			value:      "0.2.0",
			createKeys: true,
			expectedYAML: `spec:
  image:
    name: my/image
    tag: 0.2.0
`,
		},
		{
			name:       "yaml with other key and create keys",
			inputYAML:  `foo: bar`,
			fieldPath:  "spec.image.tag",
			value:      "0.2.0",
			createKeys: true,
			expectedYAML: `foo: bar
spec:
  image:
    tag: 0.2.0
`,
		},
		{
			name:       "empty yaml and create keys",
			inputYAML:  `---`,
			fieldPath:  "spec.image.tag",
			value:      "0.2.0",
			createKeys: true,
			expectedYAML: `spec:
  image:
    tag: 0.2.0
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patcher, err := yaml.NewPatcher(strings.NewReader(tt.inputYAML))
			require.NoError(t, err)

			err = patcher.SetField(strings.Split(tt.fieldPath, "."), tt.value, tt.createKeys)
			if tt.expectErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			var sb strings.Builder
			err = patcher.Encode(&sb)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedYAML, sb.String())
		})
	}
}
