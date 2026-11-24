import type { CodegenConfig } from '@graphql-codegen/cli'
import process from 'node:process'

export default {
  overwrite: true,
  schema: process.env.SCHEMA_PATH,
  documents: 'apps/web/src/**/*',
  generates: {
    'apps/web/src/schemas/gql/': {
      preset: 'client',
      config: {
        defaultScalarType: 'any',
        scalars: {
          Duration: 'string',
          Time: 'string',
        },
        skipTypename: false,
        enumsAsTypes: false,
      },
    },
  },
  hooks: { afterOneFileWrite: ['prettier -w'] },
} satisfies CodegenConfig
