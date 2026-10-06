# sper

I kept losing my ssh forwards during the day and got sick of re-running `ssh -L` by hand, so I wrote this. It just keeps `ssh -N -L ...` alive and restarts it when it drops.

Needs Go 1.21+ and `ssh` in PATH (Windows has it built in).

```
git clone https://github.com/lukaulcar/sper.git
cd sper
go build -o sper .
```

On Windows:

```
go build -o sper.exe . 
```

Put it on PATH somewhere.

## config

Make a `sper.yaml` next to the binary, in the current dir, or in `~/.config/sper/config.yaml` (`%APPDATA%\sper\config.yaml` on Windows). Example in `sper.yaml.example`:

```yaml
profiles:
  dev-db:
    server: ba
    ports:
      - 5432
      - 6379
```

Then:

```
sper dev-db
sper -list
```

If your config lives somewhere else you can do `sper -c "C:\Program Files\Sper\sper.yaml" dev-db` once, it remembers the path after that.

You can also skip the file completely, last arg is server, rest are ports:

```
sper 8080 3000 my-server
sper -i ~/.ssh/id_ed25519 5432:db.internal:5432 ba
```

Port stuff I use:

- `8080` means local 8080 -> localhost:8080
- `3000:8000` means local 3000 -> localhost:8000
- `5432:db:5432` means local 5432 -> db:5432
- `0.0.0.0:8080:host:80` to bind all interfaces

Other flags: `-p` for ssh port, `-o` for extra ssh options (can repeat), `-v` for verbose, `-healthcheck=false` to skip the local probe.

## what it does

- runs ssh with `ServerAliveInterval=10`, `ExitOnForwardFailure=yes`, etc.
- if ssh dies it sleeps 1s, 2s, 4s... up to 30s and retries. If it stayed up for 20s+ the backoff resets.
- every 5s it tries to dial the local ports, if they fail twice in a row it kills ssh so it reconnects.
- warns if the local port is already taken.
- if you don't set anything for auth it just lets ssh prompt you like normal. You can put `password:` in the profile but I wouldn't, it sits in plaintext. Use keys / agent.

MIT, see LICENSE.
