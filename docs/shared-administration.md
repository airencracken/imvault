# Shared administration helpers

This release pins Comfylib v0.1.3. Confirmed CLI password prompting and branding
image normalization use the same implementations as Songstead and the other
Comfyware companion. Comfylib disables terminal echo before either prompt is displayed and restores
the terminal afterward. Prompt labels and password strength policy remain
application-specific. Image uploads keep the existing 2 MiB,
2048 by 2048 pixel limits and PNG output. No accounts, roles, invitation policies
or database schemas are changed by this extraction.

Regression tests cover each application's existing prompts and image upload
flows; the shared library covers confirmation failures, adversarial images,
property tests, API contracts and deliberate guard mutations. The mutation
engine copy is synced with the pinned library release.
