-- Persist Cashfree checkout/session metadata so retries and status checks resolve
-- the same provider order instead of creating duplicate payment records.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS checkout_url TEXT;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS payment_session_id TEXT;

CREATE INDEX IF NOT EXISTS idx_payments_gateway_status
    ON payments(gateway_order_id, status)
    WHERE gateway_order_id IS NOT NULL;
                                                                      
❯ git pull --rebase origin main
From https://github.com/tharunsureshnaidu/wed
 * branch            main       -> FETCH_HEAD
Auto-merging marriage-hall-booking/internal/migrations/071_payment_checkout_session.sql
CONFLICT (add/add): Merge conflict in marriage-hall-booking/internal/migrations/071_payment_checkout_session.sql
error: could not apply 5bc32ea... fix
hint: Resolve all conflicts manually, mark them as resolved with
hint: "git add/rm <conflicted_files>", then run "git rebase --continue".
hint: You can instead skip this commit: run "git rebase --skip".
hint: To abort and get back to the state before "git rebase", run "git rebase --abort".
hint: Disable this message with "git config set advice.mergeConflict false"
Could not apply 5bc32ea... # fix

~/Projects/wed  main onto remotes/origin/HEAD (1/1) at @830bc08* 
❯ 