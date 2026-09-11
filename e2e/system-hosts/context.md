# Task: configure a host alias in the system hosts file

You are configuring a name alias on a machine. Work autonomously with tools.

## Environment

- On this test machine the system hosts file is at `__HOSTS_FILE__`.
- Treat that file as if it were `/etc/hosts`. Make your changes THERE.
- Never edit the real `/etc/hosts`, never use `sudo`, and never leave the
  working directory. The test verifies the real hosts file is untouched.

## What to do

1. Read the hosts file and check whether an entry already maps the address
   `192.168.0.33` to the name `mac-mini.local`.
2. If the alias is NOT configured, append exactly one line using `Write` with
   `append=true` (this preserves the existing entries; do not use `Edit` or
   overwrite the whole file):

       192.168.0.33    mac-mini.local

   Keep the existing entries unchanged.
3. If the alias IS already configured, do not add a duplicate; report that it
   is already present.
4. Read the file back to verify the final state, then report what you did.

## Rules

- The line must map `192.168.0.33` to `mac-mini.local`, separated by
  whitespace (space or tab).
- Exactly one `mac-mini.local` entry may remain in the file.
- Do not remove or rewrite the other entries.
