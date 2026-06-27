import { test, expect } from '@playwright/test'
import { createChannel, createUserViaAdmin, login, loginAsAdmin, uniqueEmail } from './helpers'

// A valid phetag: starts with a letter, lowercase, within 2–24 chars.
function uniquePhetag(): string {
  return `e2e${Date.now()}`.slice(0, 24)
}

test('owner can set a phetag and duplicates are rejected', async ({ page }) => {
  await loginAsAdmin(page)
  const phetag = uniquePhetag()

  // First channel takes the phetag.
  await createChannel(page, `Phetag ${Date.now()}`)
  await page.getByRole('tab', { name: 'Settings' }).click()
  await page.getByLabel('Phetag (channel handle)').fill(phetag)
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByText('Channel updated')).toBeVisible()

  // A second channel cannot reuse it.
  await createChannel(page, `Phetag dup ${Date.now()}`)
  await page.getByRole('tab', { name: 'Settings' }).click()
  await page.getByLabel('Phetag (channel handle)').fill(phetag)
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByText('Update failed')).toBeVisible()
})

test('a user joins by phetag and the owner approves the request', async ({ browser }) => {
  const phetag = uniquePhetag()
  const memberEmail = uniqueEmail('member')

  // Owner creates an approval-mode channel (the default) and gives it a phetag.
  const ownerCtx = await browser.newContext()
  const owner = await ownerCtx.newPage()
  await loginAsAdmin(owner)
  await createChannel(owner, `Join ${Date.now()}`)
  const channelUrl = owner.url()
  await owner.getByRole('tab', { name: 'Settings' }).click()
  await owner.getByLabel('Phetag (channel handle)').fill(phetag)
  await owner.getByRole('button', { name: 'Save changes' }).click()
  await expect(owner.getByText('Channel updated')).toBeVisible()
  await createUserViaAdmin(owner, memberEmail, 'abcd1234')

  // The member adds the channel by its phetag → pending in approval mode.
  const memberCtx = await browser.newContext()
  const member = await memberCtx.newPage()
  await login(member, memberEmail, 'abcd1234')
  await member.getByRole('button', { name: 'Add channel' }).click()
  const dialog = member.getByRole('dialog')
  await dialog.getByLabel('Trigger ID or phetag').fill(phetag)
  await dialog.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(member).toHaveURL(/\/channels\//)

  // The owner approves the pending request from the Subscribers tab.
  await owner.goto(channelUrl)
  await owner.getByRole('tab', { name: 'Subscribers' }).click()
  await expect(owner.getByText(memberEmail).first()).toBeVisible()
  await owner.getByRole('button', { name: 'Approve' }).click()
  await expect(owner.getByText('Member approved')).toBeVisible()

  await ownerCtx.close()
  await memberCtx.close()
})
