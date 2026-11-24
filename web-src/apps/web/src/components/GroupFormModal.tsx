import { zodResolver } from '@hookform/resolvers/zod'
import { useCallback, useImperativeHandle, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { useCreateGroupMutation, useGroupSetPolicyMutation } from '~/apis'
import { FormActions } from '~/components/FormActions'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '~/components/ui/dialog'
import { Input } from '~/components/ui/input'
import { Select } from '~/components/ui/select'
import { DEFAULT_GROUP_POLICY } from '~/constants'
import { useSetValue } from '~/hooks/useSetValue'
import type { Policy } from '~/schemas/gql/graphql'

const POLICY_VALUES = ['min_moving_avg', 'min_avg10', 'min', 'random', 'fixed'] as const satisfies readonly Policy[]

const schema = z.object({
  name: z.string().min(1, 'Name is required'),
  policy: z.enum(POLICY_VALUES),
  policyParams: z.array(z.object({ key: z.string(), val: z.string() })),
})

type FormValues = z.infer<typeof schema>

const defaultValues: FormValues = {
  name: '',
  policy: DEFAULT_GROUP_POLICY,
  policyParams: [],
}

export interface GroupFormModalRef {
  form: {
    setValues: (values: FormValues) => void
    reset: () => void
  }
  setEditingID: (id: string) => void
  initOrigins: (origins: FormValues) => void
}

export function GroupFormModal({
  ref,
  opened,
  onClose,
}: {
  ref?: React.Ref<GroupFormModalRef>
  opened: boolean
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [editingID, setEditingID] = useState<string>()
  const [origins, setOrigins] = useState<FormValues>()

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues,
    mode: 'onChange',
  })

  const {
    handleSubmit,
    control,
    setValue: setValueOriginal,
    reset,
    formState: { errors, isDirty },
  } = form

  const setValue = useSetValue(setValueOriginal)
  const formValues = useWatch({ control })

  const initOrigins = (origins: FormValues) => {
    reset(origins)
    setOrigins(origins)
  }

  const resetForm = useCallback(() => {
    reset(defaultValues)
  }, [reset])

  const handleClose = useCallback(() => {
    onClose()
    setTimeout(() => {
      resetForm()
      setEditingID(undefined)
      setOrigins(undefined)
    }, 200)
  }, [onClose, resetForm])

  useImperativeHandle(ref, () => ({
    form: {
      setValues: (values: FormValues) => reset(values),
      reset: resetForm,
    },
    setEditingID,
    initOrigins,
  }))

  const createGroupMutation = useCreateGroupMutation()
  const groupSetPolicyMutation = useGroupSetPolicyMutation()

  const policyData = [
    {
      label: 'min_moving_avg',
      value: 'min_moving_avg' as Policy,
      description: t('descriptions.group.MinMovingAvg'),
    },
    {
      label: 'min_avg10',
      value: 'min_avg10' as Policy,
      description: t('descriptions.group.MinAvg10'),
    },
    {
      label: 'min',
      value: 'min' as Policy,
      description: t('descriptions.group.Min'),
    },
    {
      label: 'random',
      value: 'random' as Policy,
      description: t('descriptions.group.Random'),
    },
    {
      label: 'fixed',
      value: 'fixed' as Policy,
      description: t('descriptions.group.Fixed'),
    },
  ]

  const onSubmit = async (data: FormValues) => {
    const policyParams = data.policy === 'fixed'
      ? editingID && origins?.policy === 'fixed'
        ? origins.policyParams
        : [{ key: '', val: '0' }]
      : []

    if (editingID) {
      await groupSetPolicyMutation.mutateAsync({
        id: editingID,
        policy: data.policy,
        policyParams,
      })
    } else {
      await createGroupMutation.mutateAsync({
        name: data.name,
        policy: data.policy,
        policyParams,
      })
    }

    handleClose()
  }

  return (
    <Dialog open={opened} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('group')}</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit(onSubmit)}>
          <div className="space-y-4">
            <Input
              withAsterisk
              label={t('name')}
              value={formValues.name}
              onChange={(e) => setValue('name', e.target.value)}
              error={errors.name?.message}
              disabled={!!editingID}
            />

            <Select
              label={t('policy')}
              data={policyData}
              value={formValues.policy}
              onChange={(value) => setValue('policy', value as Policy)}
            />

            <FormActions
              reset={() => {
                if (editingID && origins) {
                  reset(origins)
                } else {
                  resetForm()
                }
              }}
              isDirty={isDirty}
              errors={errors}
            />
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}
