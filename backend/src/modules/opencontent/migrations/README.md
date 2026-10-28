# Open Content migrations

Goose `.sql` migrations for this module, embedded in the server binary. They run
after core's, and their versions are tracked in `public.goose_db_version_opencontent`.

Never edit a migration that has been merged; add a new one.

Create new tables as `open_content.<name>`. Migrations run on core's
connection, so an unqualified table lands in `public`, where the
`open_content` role can't use it.
