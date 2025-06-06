---
include:
  - project: '{{ .ProjectPath }}'
    ref: '{{ .CommitSHA }}'
    file: '.gitlab/benchmarks.yml'

stages:
  - benchmark_matrix

benchmark_matrix:
  extends: .benchmark
  stage: benchmark_matrix
  parallel:
    matrix:
      - BENCHMARK_NAME:
{{- range .BenchmarkNames }}
        - "{{ . }}"
{{- end }} 
