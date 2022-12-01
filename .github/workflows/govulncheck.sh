govulncheck $CHECK_DIR >> ddtrace_results.txt
if [ $(cat ddtrace_results.txt | grep "Vulnerability #" | wc -l) -gt 0 ]; then
  echo "Found ${num} vulnerabilities"
  echo $(cat ddtrace_results.txt | grep "Vulnerability #")
  exit 1
fi