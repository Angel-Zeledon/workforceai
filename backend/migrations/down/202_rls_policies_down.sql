-- Rollback of 202_rls_policies.sql (disables RLS on all tenant tables).
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'organizations', 'agents', 'tasks', 'requests', 'conversations', 'messages',
        'approvals', 'reports', 'memories', 'events', 'audit_logs', 'activity'
    ]
    LOOP
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('ALTER TABLE %I NO FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I DISABLE ROW LEVEL SECURITY', t);
    END LOOP;
END
$$;
DROP FUNCTION IF EXISTS app_current_org();
