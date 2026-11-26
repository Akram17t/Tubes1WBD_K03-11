import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { api } from '../../api/client.js'
import { ConfirmButton } from '../../components/ConfirmButton.jsx'
import { PageHeader } from '../../components/PageHeader.jsx'
import { ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useAction, useApi } from '../../hooks/useApi.js'
import { GroupGrants } from './GroupGrants.jsx'
import { GroupMembers } from './GroupMembers.jsx'

// detail group: info dasar, anggota + leader, dan akses database
export function GroupDetail() {
  const { groupId } = useParams()
  const group = useApi((signal) => api.get(`/admin/groups/${groupId}`, null, { signal }), [groupId])

  if (group.loading && !group.data) return <Loading />
  if (group.error) return <ErrorAlert error={group.error} onRetry={group.reload} />

  return (
    <>
      <PageHeader title={group.data.name} back={{ to: '/admin/groups', label: 'Daftar group' }} />
      <GroupInfo group={group.data} onSaved={group.reload} />
      <GroupMembers group={group.data} onLeaderChanged={group.reload} />
      <GroupGrants groupId={groupId} />
    </>
  )
}

// edit nama/deskripsi + arsipkan group
function GroupInfo({ group, onSaved }) {
  const navigate = useNavigate()
  const [form, setForm] = useState({ name: group.name, description: group.description || '' })
  const save = useAction()
  const archive = useAction()
  const [saved, setSaved] = useState(false)

  useEffect(() => setForm({ name: group.name, description: group.description || '' }), [group])

  async function handleSave(event) {
    event.preventDefault()
    setSaved(false)
    try {
      await save.run(() => api.patch(`/admin/groups/${group.id}`, { name: form.name, description: form.description.trim() || null }))
      setSaved(true)
      onSaved()
    } catch {
      /* error tampil lewat save.error */
    }
  }

  const handleArchive = () =>
    archive.run(async () => {
      await api.del(`/admin/groups/${group.id}`)
      navigate('/admin/groups')
    })

  return (
    <section className="card mb-4">
      <div className="card-body">
        <h2 className="h5">Informasi group</h2>
        <ErrorAlert error={save.error || archive.error} />
        {saved && <div className="alert alert-success py-2">Perubahan disimpan.</div>}
        <form className="row g-2 align-items-end" onSubmit={handleSave}>
          <div className="col-md-4">
            <label htmlFor="name" className="form-label">
              Nama
            </label>
            <input id="name" className="form-control" required value={form.name} onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))} />
          </div>
          <div className="col-md-6">
            <label htmlFor="description" className="form-label">
              Deskripsi
            </label>
            <input
              id="description"
              className="form-control"
              value={form.description}
              onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
            />
          </div>
          <div className="col-md-2 d-grid">
            <button type="submit" className="btn btn-primary" disabled={save.busy}>
              {save.busy ? 'Menyimpan...' : 'Simpan'}
            </button>
          </div>
        </form>
        <hr />
        <div className="d-flex flex-wrap align-items-center gap-2">
          <span className="small text-secondary me-auto">
            Mengarsipkan group mencabut semua akses database dan session anggotanya. Riwayat yang merujuk group ini tetap tersimpan.
          </span>
          <ConfirmButton busy={archive.busy} onConfirm={handleArchive} confirmText="Arsipkan">
            Arsipkan group
          </ConfirmButton>
        </div>
      </div>
    </section>
  )
}
