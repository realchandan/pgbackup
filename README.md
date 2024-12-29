# PostgreSQL Incremental Scheduled Backups to S3-Compatible Storage

I was frustrated by how surprisingly difficult it is to set up PostgreSQL backups in a Docker-based setup. With PostgreSQL 17, the built-in backup tool `pg_basebackup` introduced support for incremental backups.

I decided to create a tool that leverages `pg_basebackup` to generate incremental backups and store them in S3-compatible storage. That’s what this project is all about. It’s an incredibly easy-to-use tool written in Golang with fewer than 400 lines of code, and it does exactly what it promises.

## Usage

`pgbackup` is primarily intended for local Docker-based setups, but you can also build it as a binary and supply the necessary inputs to make it work with almost any PostgreSQL setup. Keep in mind that support for incremental backups was introduced in PostgreSQL 17, so this tool won’t work with any version prior to that.

You can use the Docker image, provide the relevant environment variables, and it will work seamlessly.

Here is the list of environment variables `pgbackup` requires. Note that every environment variable must be provided, as there are no default values:

| Variable Name     | Explanation                                                                                                        |
| ----------------- | ------------------------------------------------------------------------------------------------------------------ |
| DB_HOST           | The hostname of the target database.                                                                               |
| DB_PORT           | The port of the target database.                                                                                   |
| REMOTE_FOLDER     | The name of the S3 bucket folder where backups will be stored.                                                     |
| S3_ACCESS_KEY     | Self-explanatory.                                                                                                  |
| S3_BUCKET_NAME    | Self-explanatory.                                                                                                  |
| S3_ENDPOINT       | Self-explanatory.                                                                                                  |
| S3_REGION         | Self-explanatory.                                                                                                  |
| S3_SECRET_KEY     | Self-explanatory.                                                                                                  |
| SCHEDULE          | The cron schedule for creating backups. The minimum frequency is every 5 minutes, and the maximum is once per day. |
| POSTGRES_PASSWORD | The password for the PostgreSQL superuser.                                                                         |
| POSTGRES_USER     | The username for the PostgreSQL superuser.                                                                         |

## Restoring Backups

It’s not every day that you’ll need to restore a database. Restoring a backup is a manual process, and there are no plans to include that functionality in this repository.

If disaster strikes and you need to recover your database, here are the steps:

1. Download all the backup folders from S3 or S3-compatible storage. You can use the AWS CLI’s `aws s3 sync` command for this.

2. Use `pg_combinebackup` to combine all the incremental backups into a single backup that can be restored. In short, the steps involve untarring the snapshot files, arranging the backups from oldest to newest, and running `pg_combinebackup` with the appropriate command-line arguments. You can read more about the process [here](https://www.depesz.com/2024/01/08/waiting-for-postgresql-17-add-support-for-incremental-backup/).
