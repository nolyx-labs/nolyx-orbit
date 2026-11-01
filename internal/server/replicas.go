// Read replicas: create, promote, re-sync.
package server

import (
	"context"
	"net/http"
	"orbit/internal/protocol"

	"github.com/jackc/pgx/v5"
)

func (d dbRow) replicaArgs(primary dbRow, name string, port int) protocol.PGArgs {
	return protocol.PGArgs{Name: name, Version: majorVersion(primary.Version), Port: port, Password: primary.Password,
		ReplPassword: primary.ReplPassword, PrimaryHost: primary.reachableHost(), PrimaryPort: primary.Port}
}

var replicaPlan = []string{"Pulling image", "Creating container", "Cloning primary (pg_basebackup)", "Waiting for readiness"}

func (s *Server) createReplica(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	primary, err := s.loadDB(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	var in dbInput
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := in.validate(false); err != nil {
		return err
	}
	if primary.Role != "primary" {
		return errStatus(400, "replicas can only be created from a primary")
	}
	if primary.ReplPassword == "" {
		return errStatus(400, "the primary has no replication credentials: create a REPLICATION role named orbit_replicator, allow it in pg_hba.conf, and set its password on the database")
	}
	if err := s.checkServer(r.Context(), p, in.ServerID); err != nil {
		return err
	}
	if !s.hub.connected(in.ServerID) {
		return errOffline
	}
	var dbID string
	err = s.db.QueryRow(r.Context(), `insert into databases (project_id, server_id, name, host, port, username, dbname, sslmode, password_encrypted,
		repl_password_encrypted, managed, version, status, role, primary_id)
		values ($1,$2,$3,'127.0.0.1',$4,$5,$6,'disable',$7,$8,true,$9,'provisioning','replica',$10) on conflict do nothing returning id::text`,
		p.ID, in.ServerID, in.Name, in.Port, primary.Username, primary.DBName, s.encrypt([]byte(primary.Password)), s.encrypt([]byte(primary.ReplPassword)),
		majorVersion(primary.Version), primary.ID).Scan(&dbID)
	if err == pgx.ErrNoRows {
		return errStatus(409, "a database named %s already exists", in.Name)
	} else if err != nil {
		return err
	}
	args := primary.replicaArgs(primary, in.Name, in.Port)
	args.CPUs, args.MemoryMB = in.CPUs, in.MemoryMB
	s.auditProject(r, p, "database.replica.create", in.Name, map[string]any{"primary": primary.Name, "server_id": in.ServerID})
	opID, err := s.startOperation(r, p, "db.replica.create", in.Name, in.ServerID, dbID, replicaPlan, protocol.ActionDBReplicaCreate, args)
	if err != nil {
		s.db.Exec(context.WithoutCancel(r.Context()), `update databases set status = 'failed', error = $2 where id = $1`, dbID, err.Error())
		return err
	}
	writeJSON(w, 202, map[string]string{"id": dbID, "operation_id": opID})
	return nil
}

func (s *Server) promoteDatabase(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	d, err := s.loadDB(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := confirmName(r, d.Name); err != nil {
		return err
	}
	if d.Role != "replica" {
		return errStatus(400, "only replicas can be promoted")
	}
	s.auditProject(r, p, "database.promote", d.Name, nil)
	opID, err := s.startOperation(r, p, "db.promote", d.Name, d.ServerID, d.ID, nil, protocol.ActionDBPromote, protocol.DBArgs{DatabaseID: d.ID})
	if err != nil {
		return err
	}
	writeJSON(w, 202, map[string]string{"operation_id": opID})
	return nil
}

func (s *Server) resyncDatabase(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	d, err := s.loadDB(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := confirmName(r, d.Name); err != nil {
		return err
	}
	if !d.Managed || d.PrimaryID == nil {
		return errStatus(400, "re-sync is available for Orbit-managed replicas only")
	}
	primary, err := s.loadDB(r.Context(), p, *d.PrimaryID)
	if err != nil {
		return errStatus(400, "the replica's primary no longer exists")
	}
	s.db.Exec(r.Context(), `update databases set status = 'provisioning', error = '' where id = $1`, d.ID)
	s.pushConfig(r.Context(), d.ServerID)
	s.auditProject(r, p, "database.resync", d.Name, map[string]any{"primary": primary.Name})
	opID, err := s.startOperation(r, p, "db.resync", d.Name, d.ServerID, d.ID, append([]string{"Removing container"}, replicaPlan...),
		protocol.ActionDBResync, d.replicaArgs(primary, d.Name, d.Port))
	if err != nil {
		return err
	}
	writeJSON(w, 202, map[string]string{"operation_id": opID})
	return nil
}
