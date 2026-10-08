# Shared CSRF derivation

The application now calls `comfylib/token.SessionCSRF` and pins published
Comfylib v0.1.1 with verified module checksums. Its existing purpose string is retained, so previously
issued sessions continue to use the same CSRF token. Independent compatibility
vectors cover that contract.

Develop with a sibling Comfylib worktree selected by an untracked `go.work`:

```sh
go work init . ../comfylib
go work edit -replace=github.com/airencracken/comfylib@v0.1.1=../comfylib
```

Release checks run with `GOWORK=off` and resolve the published library through
the module proxy. Keep workspace files and local replacements out of commits.
The copied mutation engine is unchanged apart from its version header.
