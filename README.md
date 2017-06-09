# testpipe
Concourse Pipeline Tester

## Current features
- [x] Ensure parity of params between task config and pipeline config that uses the task
- [x] Ensure that all task inputs are satisfied
- [ ] Ensure no invalid keys are passed to `get` (`params:` is often forgotten and keys on the `get` are silently ignored)

## Installation

`go get github.com/krishicks/testpipe/cmd/testpipe`

## Usage

### Setup
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
      foo: bar # <- extra param the task does not require
EOF

mkdir -p $dir/some-resource

cat > $dir/some-resource/task.yml <<EOF
---
params:
  baz: # <- param that the task usage does not specify
EOF

cat > $dir/config.yml <<EOF
resource_map:
  some-resource: $dir/some-resource
EOF
```

### Execute
```
testpipe -p $dir/pipeline.yml -c $dir/config.yml
```

### Output
```
Params do not have parity:
  Pipeline:     /tmp/tmp.xPl0PMmQMa/pipeline.yml
  Job:          a-job
  Task:         a-task

  Extra fields that should be removed:
    foo

  Missing fields that should be added:
    baz
```
