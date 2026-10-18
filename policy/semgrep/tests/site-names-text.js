// ok: no-workflow-or-task-picks-a-site-by-name
// site0 only, in a comment
// ruleid: no-workflow-or-task-picks-a-site-by-name
const site = 'site0';
// ruleid: no-workflow-or-task-picks-a-site-by-name
const fallback = `${{ inputs.site || 'site0' }}`;
// ok: no-workflow-or-task-picks-a-site-by-name
const told = `${{ matrix.site }}`;
