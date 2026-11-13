# Setting up Imvault

Imvault uses the same browser setup and account commands as the other
Comfyware apps.

Configure the public HTTPS origin and listener first. Then create the first
owner in a browser or at a terminal. Installed commands find the service's data
directory and re-run as its account when invoked with sudo. On a portable
install, run them as the account that owns the data directory.

## Browser

```sh
sudo imvault setup
```

The command prints the setup address and a private setup key. Start the
service, open `/setup`, and enter the key with a username and password. A
browser on the host may use the loopback HTTP address; anywhere else needs
HTTPS. The page works without JavaScript.

Keep the key private. It never appears in a URL or the startup log. The page
closes once any owner exists, including one made at the command line, and it
never replaces an account.

## Command line

```sh
sudo imvault create-owner --username yourname
sudo imvault set-password --username yourname
```

Both commands prompt twice with echo off. For automation, pass
`--password-stdin` or `--password-file /private/path/password`; the file must
be a private regular file. `--data-dir DIRECTORY` selects a portable instance.
Passwords need at least 12 characters and at most 72 UTF-8 bytes.

`create-owner` never promotes or overwrites an existing username;
`create-admin` is an alias for it. `set-password` signs the account out everywhere
and cancels its pending recovery links.

Run `imvault help setup` for command help. Invitations and site settings are in
the app once you sign in.
