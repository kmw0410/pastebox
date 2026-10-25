const { test, expect } = require('@playwright/test');

async function upload(request, content, headers = {}) {
  const response = await request.post('/?format=json', {
    data: content,
    headers: { 'content-type': 'text/plain', ...headers },
  });
  expect(response.status()).toBe(200);
  return (await response.json()).url;
}

test('search, left line numbers, line links and content-only copy', async ({ page, request }) => {
  const content = 'alpha\nbeta alpha\ngamma';
  const url = await upload(request, content);
  await page.goto(url);
  await expect(page.locator('#lineNumbers')).toBeHidden();
  await page.locator('#lineToggle').click();
  const gutter = await page.locator('#lineNumbers').boundingBox();
  const code = await page.locator('#pasteContent').boundingBox();
  expect(gutter.x + gutter.width).toBeLessThanOrEqual(code.x + 1);
  await page.locator('#lineNumbers a[data-line="2"]').click();
  await expect(page).toHaveURL(/#L2$/);
  await expect(page.locator('#lineNumbers a[data-line="2"]')).toHaveClass(/is-active/);
  await page.reload();
  await expect(page.locator('#lineNumbers a[data-line="2"]')).toHaveClass(/is-active/);
  await page.locator('#searchButton').click();
  await page.locator('#pasteSearchInput').fill('alpha');
  await expect(page.locator('#pasteSearchCount')).toHaveText('1 / 2');
  await page.locator('#pasteSearchNext').click();
  await expect(page.locator('#pasteSearchCount')).toHaveText('2 / 2');
  await page.locator('#copyButton').click();
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(content);
  await page.locator('#pasteSearchClose').click();
  await expect(page.locator('#pasteContent')).toHaveText(content);
});

for (const policy of ['permanent', 'once']) {
  test(`confirmed full loading preserves ${policy} content`, async ({ page, request }) => {
    const content = Array.from({ length: 450 }, (_, i) => `line ${i + 1}`).join('\n');
    const url = await upload(request, content, { 'data-policy': policy });
    await page.goto(url);
    await expect(page.locator('#remainingLinesNotice')).toBeVisible();
    page.once('dialog', dialog => dialog.dismiss());
    await page.locator('#loadFullButton').click();
    await expect(page.locator('#remainingLinesNotice')).toBeVisible();
    page.once('dialog', dialog => dialog.accept());
    await page.locator('#loadFullButton').click();
    await expect(page.locator('#loadFullButton')).toHaveCount(0);
    await expect(page.locator('#pasteContent')).toHaveText(content);
    await page.locator('#copyButton').click();
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(content);
    if (policy === 'once') expect((await request.get(url)).status()).toBe(404);
  });
}
