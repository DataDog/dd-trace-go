---
include:
  - project: '{{ .ProjectPath }}'
    ref: '{{ .CommitSHA }}'
    file: '.gitlab/benchmarks.yml'

workflow:
  auto_cancel:
    on_new_commit: interruptible

stages:
  - benchmark_matrix

benchmark_matrix:
  extends: .benchmark
  stage: benchmark_matrix
  rules:
    - if: $CI_COMMIT_BRANCH == "main" || $CI_COMMIT_BRANCH =~ /^[0-9]+\.[0-9]+$/
      interruptible: false
    - interruptible: true
  parallel:
    matrix:
      - BENCHMARK_NAME:
{{- range .BenchmarkNames }}
        - "{{ . }}"
{{- end }} 
