BEGIN;

ALTER TABLE groups
    ADD COLUMN leader_user_id BIGINT,
    ADD COLUMN is_archived BOOLEAN NOT NULL DEFAULT false,
    ADD CONSTRAINT groups_leader_membership_fkey
        FOREIGN KEY (leader_user_id, id) REFERENCES user_groups (user_id, group_id)
        ON DELETE SET NULL (leader_user_id),
    ADD CONSTRAINT groups_archived_without_leader CHECK (NOT is_archived OR leader_user_id IS NULL);

CREATE INDEX groups_leader_user_id_idx ON groups (leader_user_id) WHERE leader_user_id IS NOT NULL;

COMMIT;
