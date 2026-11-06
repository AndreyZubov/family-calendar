import { expect, test } from '@playwright/test'

import { createFamily, signIn, todayKey } from './helpers'

test('a signed-in user plans, edits and deletes an event', async ({ page }) => {
  await signIn(page, 'Alice Smith')

  // First run: no family yet.
  await expect(page.getByRole('heading', { name: 'Create your family' })).toBeVisible()
  await createFamily(page, 'The Smiths')
  await expect(page.getByRole('link', { name: 'The Smiths' })).toBeVisible()
  await page.screenshot({ path: 'test-results/screens/01-empty-calendar.png', fullPage: true })

  // Create an event for today.
  await page.getByRole('link', { name: 'New event' }).click()
  await page.getByLabel('Title').fill('Family dinner')
  await page.getByLabel('Start time').fill('18:00')
  await page.getByLabel('End time').fill('20:00')
  await page.getByLabel('Notes').fill('At grandma’s')
  await expect(page.getByRole('checkbox', { name: 'Alice Smith' })).toBeChecked() // author by default
  await page.screenshot({ path: 'test-results/screens/02-event-form.png', fullPage: true })
  await page.getByRole('button', { name: 'Create event' }).click()

  // Back on the calendar, the event shows in the grid and in today's list.
  await expect(page).toHaveURL(new RegExp(`/\\?day=${todayKey()}$`))
  const dayList = page.getByRole('region', { name: /Events on/ })
  const row = dayList.getByRole('link', { name: /Family dinner/ })
  await expect(row).toBeVisible()
  await expect(row).toContainText('Family dinner')
  await expect(row.getByLabel('1 participant', { exact: true })).toBeVisible()
  await expect(page.getByRole('grid').getByText('Family dinner')).toBeVisible()
  await page.screenshot({ path: 'test-results/screens/03-calendar-with-event.png', fullPage: true })

  // It survives a reload (it is really stored on the server).
  await page.reload()
  await expect(dayList.getByRole('link', { name: /Family dinner/ })).toBeVisible()

  // Edit.
  await dayList.getByRole('link', { name: /Family dinner/ }).click()
  await expect(page.getByLabel('Title')).toHaveValue('Family dinner')
  await expect(page.getByLabel('Notes')).toHaveValue('At grandma’s')
  await page.getByLabel('Title').fill('Late family dinner')
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(dayList.getByRole('link', { name: /Late family dinner/ })).toBeVisible()

  // Validation from the client: an empty title is not sent.
  await dayList.getByRole('link', { name: /Late family dinner/ }).click()
  await page.getByLabel('Title').fill('')
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByRole('alert')).toHaveText('Give the event a title.')

  // Delete.
  page.once('dialog', (d) => void d.accept())
  await page.getByRole('button', { name: 'Delete' }).click()
  await expect(dayList.getByText('Nothing planned.')).toBeVisible()
})

test('signing out ends the session', async ({ page }) => {
  await signIn(page, 'Alice Smith')
  await expect(page.getByRole('grid')).toBeVisible() // family from the previous test

  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page.getByRole('link', { name: 'Sign in with Google' })).toBeVisible()

  // The server revoked the session: going back does not get in.
  await page.goto('/family')
  await expect(page).toHaveURL(/\/login\?return_to=%2Ffamily/)
})
