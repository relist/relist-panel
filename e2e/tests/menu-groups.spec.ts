import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { sandbox } from '../lib/sandbox.js';

// An admin's sider groups the host-wide pages under titles (GUIDELINES §3).
// A group is a label, not a page: items still open in one click, and the
// breadcrumb never names the group.

const groups: [string | null, [string, string][]][] = [
  [null, [['Overview', '/'], ['Projects', '/projects'], ['Deployments', '/deployments']]],
  ['Host', [['Monitoring', '/monitoring'], ['Docker', '/docker']]],
  ['Build & images', [['Images', '/images'], ['Registry', '/registry'], ['Builders', '/builders'], ['SSH keys', '/ssh-keys']]],
  ['Data', [['Databases', '/databases']]],
  ['Routing', [['Traefik', '/traefik'], ['Certificates', '/certificates'], ['Basic auth', '/basic-auth']]],
  ['Security', [['Vulnerabilities', '/vulnerabilities'], ['Audit log', '/audit']]],
  ['Panel', [['Users', '/users'], ['Panel update', '/panel-update']]],
];

async function signIn(page: Page) {
  const env = sandbox();
  await page.goto('/signin');
  await page.getByLabel(/email/i).fill(env.adminEmail);
  await page.getByLabel(/password/i).first().fill(env.adminPassword);
  await page.getByRole('button', { name: /sign in/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/signin'));
  await expect(page.getByRole('menuitem').first()).toBeVisible();
}

test('an admin sees the menu groups in order', async ({ page }) => {
  await signIn(page);
  const titles = page.getByRole('navigation', { name: 'Main navigation' }).locator('.ant-menu-item-group-title');
  await expect(titles).toHaveText([...groups.flatMap(([title]) => (title ? [title] : [])), 'Recent Projects']);
  for (const [title, items] of groups.slice(1)) {
    const group = page.locator('.ant-menu-item-group').filter({ has: page.locator('.ant-menu-item-group-title', { hasText: title! }) });
    await expect(group.getByRole('menuitem')).toHaveText(items.map(([label]) => label));
  }
});

test('every grouped item opens its page in one click', async ({ page }) => {
  await signIn(page);
  for (const [, items] of groups) {
    for (const [label, path] of items) {
      // The name carries the icon's name first and maybe a Critical badge after.
      const item = page.getByRole('menuitem', { name: new RegExp(`\\b${label}\\b`) });
      await item.click();
      await expect(page).toHaveURL((u) => u.pathname === path);
      await expect(item).toHaveClass(/ant-menu-item-selected/);
      await expect(page.getByText("You don't have permission")).toHaveCount(0);
    }
  }
  await expect(page.locator('.ant-breadcrumb')).toHaveText(/^Overview\s*\/\s*Panel update$/);
});
