import { afterEach, expect, test, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import ProjectPage from './Project'
import { api, type Project } from '../lib/api'

afterEach(() => vi.restoreAllMocks())

test('review: failed run-status query must not reopen legacy generation actions', async () => {
  const project: Project = {
    id: 'review-project', title: 'review project', idea: 'synthetic',
    aspect: '16:9', totalSeconds: 5, status: 'DRAFT', budgetCredits: 500,
    spentCredits: 0, keyvisualUrl: '', finalUrl: '', errorMsg: '',
    createdAt: '', updatedAt: '', characters: [], scenes: [], shots: [], reviews: [],
  }
  vi.spyOn(api, 'get').mockImplementation(async (path) => {
    if (path.endsWith('/runs/latest')) throw new Error('synthetic run-state query unavailable')
    return project
  })
  render(<MemoryRouter initialEntries={['/project/review-project']}>
    <Routes><Route path="/project/:id" element={<ProjectPage />} /></Routes>
  </MemoryRouter>)
  await screen.findByText('review project')
  // An unavailable query is not proof of an old project or absent run. Keep
  // side-effect actions closed until authoritative run state has been loaded.
  expect(screen.queryByRole('button', { name: '生成剧本' })).toBeNull()
  expect(screen.queryByText('历史项目')).toBeNull()
})
