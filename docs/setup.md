# Setting up Imvault

Imvault shares its browser setup and account commands with the other
Comfyware apps.

Configure your service's public HTTPS origin and listener, then choose either
browser setup or a terminal prompt. Installed commands find the service's data
directory and re-run as its account when invoked with sudo. Portable installs
run these commands as the unprivileged account that owns their data directory.

## Browser

```sh
sudo imvault setup
```

The command prints the setup address and a private setup key. Start the service,
open `/setup`, and enter that key with your chosen username and confirmed
password. A browser on the host may use its loopback HTTP address; external
setup uses HTTPS. The page works without JavaScript, on mobile and desktop,
and with system light and dark themes. Keep the key private; it is never part
of a URL or server startup log. The setup page closes when any owner exists,
including one created through the command line. It never replaces an account.

## Command line

```sh
sudo imvault create-owner --username yourname
sudo imvault set-password --username yourname
```

Both commands accept `--data-dir DIRECTORY` to select a portable instance.
Without it, they discover the installed service or use the app’s data-directory
environment variable. Both commands prompt twice with echo disabled. Automation may explicitly use
`--password-stdin` or `--password-file /private/path/password`; the file must be
a private regular file. `--password-prompt` remains supported. New passwords
need at least 12 characters and at most 72 UTF-8 bytes. Existing usernames are
never promoted or overwritten by owner creation. A password reset revokes the
account's earlier sessions.

Use `imvault help setup` or `imvault setup --help` for command help. Application-specific
settings and invitations are available after signing in. Existing command names
remain supported for compatibility.

Future Bubble Tea administration panels should call the same account and
settings operations as these commands, retaining their validation and session
revocation rules. Interactive panels supplement the commands used by automation.
