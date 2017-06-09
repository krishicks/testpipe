# testpipe
Concourse Pipeline Tester

## Installation

`go get github.com/krishicks/testpipe/cmd/testpipe`

## Usage

```
dir=$(mktemp -d)

cat > $dir/pipeline.yml <<EOF
resources:
- name: some-resource

jobs:
- name: a-job
  plan:
  - task: a-task
    file: some-resource/task.yml
    params:
      foo: bar
EOF

mkdir -p $dir/some-resource

cat > $dir/some-resource/task.yml <<EOF
---
params:
  baz: quux
EOF

cat > $dir/config.yml <<EOF
resource_map:
  "some-resource": $dir/some-resource
EOF

testpipe -p $dir/pipeline.yml -c $dir/config.yml
Params do not have parity:
  Pipeline:     /tmp/tmp.xPl0PMmQMa/pipeline.yml
  Job:          a-job
  Task:         a-task

  Extra fields that should be removed:
    foo

  Missing fields that should be added:
    baz
```
