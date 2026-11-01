// PostgreSQL backups and restores via helper containers, with optional S3.
package agent

import (
	"context"
	"fmt"
	"orbit/internal/protocol"
	"strconv"
	"strings"
)

func (a *Agent) pgClient(args protocol.BackupArgs, script string, extraEnv ...string) map[string]any {
	return map[string]any{
		"Image":      "postgres:" + args.Version,
		"Entrypoint": []string{"bash", "-c", script},
		"Env": append([]string{"PGPASSWORD=" + args.DB.Password, "PGHOST=" + args.DB.Host, "PGPORT=" + strconv.Itoa(args.DB.Port),
			"PGUSER=" + args.DB.User, "PGDATABASE=" + args.DB.DBName, "FILE=/backups/" + args.File}, extraEnv...),
		"HostConfig": map[string]any{"NetworkMode": "host", "Mounts": []map[string]any{{"Type": "volume", "Source": "orbit-backups", "Target": "/backups"}}},
	}
}

func s3Env(s *protocol.S3Target) []string {
	return []string{"AWS_ACCESS_KEY_ID=" + s.AccessKey, "AWS_SECRET_ACCESS_KEY=" + s.SecretKey, "AWS_DEFAULT_REGION=" + s.Region}
}

func s3Body(s *protocol.S3Target, args []string) map[string]any {
	if s.Endpoint != "" {
		args = append(args, "--endpoint-url", s.Endpoint)
	}
	return map[string]any{
		"Image": "amazon/aws-cli:2.17.0", "Cmd": args, "Env": s3Env(s),
		"HostConfig": map[string]any{"NetworkMode": "host", "Mounts": []map[string]any{{"Type": "volume", "Source": "orbit-backups", "Target": "/backups"}}},
	}
}

func s3URI(s *protocol.S3Target, file string) string {
	return "s3://" + s.Bucket + "/" + strings.Trim(strings.TrimSuffix(s.Prefix, "/")+"/"+file, "/")
}

// pgBackup writes a pg_dump custom-format file to the orbit-backups volume and optionally uploads it.
func (a *Agent) pgBackup(ctx context.Context, args protocol.BackupArgs, progress func(protocol.Progress)) (any, error) {
	if !fileRe.MatchString(args.File) || !pgVersionRe.MatchString(args.Version) {
		return nil, fmt.Errorf("invalid backup arguments")
	}
	step := stepper(progress)
	var size int64
	if err := step("Dumping database", func() error {
		code, out, err := a.docker.Run(ctx, "orbit-backup-"+strings.TrimSuffix(args.File, ".dump"),
			a.pgClient(args, `set -e; pg_dump -Fc -f "$FILE"; stat -c %s "$FILE"`))
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("pg_dump exited %d:\n%s", code, tail(out, 10))
		}
		lines := strings.Fields(out)
		if len(lines) > 0 {
			size, _ = strconv.ParseInt(lines[len(lines)-1], 10, 64)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	location := "volume orbit-backups/" + args.File
	if args.S3 != nil {
		if err := step("Uploading to S3", func() error {
			code, out, err := a.docker.Run(ctx, "orbit-upload-"+strings.TrimSuffix(args.File, ".dump"),
				s3Body(args.S3, []string{"s3", "cp", "/backups/" + args.File, s3URI(args.S3, args.File), "--only-show-errors"}))
			if err == nil && code != 0 {
				err = fmt.Errorf("upload failed:\n%s", tail(out, 10))
			}
			return err
		}); err != nil {
			return nil, err
		}
		location = s3URI(args.S3, args.File)
	}
	if len(args.Prune) > 0 {
		step("Applying retention", func() error {
			files := []string{}
			for _, f := range args.Prune {
				if fileRe.MatchString(f) && f != args.File {
					files = append(files, "/backups/"+f)
				}
			}
			if len(files) == 0 {
				return nil
			}
			body := a.pgClient(args, "")
			body["Entrypoint"] = append([]string{"rm", "-f", "--"}, files...)
			if _, _, err := a.docker.Run(ctx, "orbit-prune-"+strings.TrimSuffix(args.File, ".dump"), body); err != nil {
				return err
			}
			for _, f := range args.Prune {
				if args.S3 != nil && fileRe.MatchString(f) {
					a.docker.Run(ctx, "orbit-prune-s3-"+strings.TrimSuffix(f, ".dump"), s3Body(args.S3, []string{"s3", "rm", s3URI(args.S3, f), "--only-show-errors"}))
				}
			}
			return nil
		})
	}
	return map[string]any{"size": size, "location": location}, nil
}

func (a *Agent) pgRestore(ctx context.Context, args protocol.BackupArgs, progress func(protocol.Progress)) error {
	if !fileRe.MatchString(args.File) || !pgVersionRe.MatchString(args.Version) {
		return fmt.Errorf("invalid restore arguments")
	}
	step := stepper(progress)
	if args.S3 != nil {
		if err := step("Downloading from S3", func() error {
			code, out, err := a.docker.Run(ctx, "orbit-download-"+strings.TrimSuffix(args.File, ".dump"),
				s3Body(args.S3, []string{"s3", "cp", s3URI(args.S3, args.File), "/backups/" + args.File, "--only-show-errors"}))
			if err == nil && code != 0 {
				err = fmt.Errorf("download failed:\n%s", tail(out, 10))
			}
			return err
		}); err != nil {
			return err
		}
	}
	return step("Restoring (pg_restore --clean)", func() error {
		code, out, err := a.docker.Run(ctx, "orbit-restore-"+strings.TrimSuffix(args.File, ".dump"),
			a.pgClient(args, `pg_restore --clean --if-exists --no-owner --exit-on-error -d "$PGDATABASE" "$FILE"`))
		if err == nil && code != 0 {
			err = fmt.Errorf("pg_restore exited %d:\n%s", code, tail(out, 10))
		}
		return err
	})
}
