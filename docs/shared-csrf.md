# Shared CSRF derivation

The application now calls `comfylib/token.SessionCSRF` and pins the pending
Comfylib v0.1.1 release. Its existing purpose string is retained, so previously
issued sessions continue to use the same CSRF token. Independent compatibility
vectors cover that contract.

Develop with a sibling Comfylib worktree selected by an untracked `go.work`:

```sh
go work init . ../comfylib
go work edit -replace=github.com/airencracken/comfylib@v0.1.1=../comfylib
```

Publish Comfylib v0.1.1 first, download the actual release to record its checksums,
then run the complete release checks with `GOWORK=off`. Until the tag is
published, tests that deliberately resolve the released library through the
module proxy cannot run. Keep workspace files and local replacements out of
commits. The copied mutation engine is unchanged apart from its version header.
