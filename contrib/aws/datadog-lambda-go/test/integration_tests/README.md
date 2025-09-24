# Integration tests


## Requirements

- Node
- Go
- DD_API_KEY

## Running

```bash
DD_API_KEY=<API_KEY> aws-vault exec sandbox-account-admin -- ./run_integration_tests.sh
```

Use `UPDATE_SNAPSHOTS=true` to update snapshots
