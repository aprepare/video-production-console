export type ImageGenerationOutput = {
  id: string
  position: number
  status: 'pending' | 'ready' | 'failed'
  mime_type: string
  width: number
  height: number
  bytes: number
  error: string
  url: string
};

export type ImageGeneration = {
  id: string
  prompt: string
  final_prompt: string
  enhance: boolean
  style: string
  custom_style: string
  model: string
  ratio: '3:4' | '4:3' | '9:16' | '1:1'
  count: number
  status: 'queued' | 'running' | 'completed' | 'partial' | 'failed'
  error: string
  favorite: boolean
  created_at: string
  updated_at: string
  outputs: ImageGenerationOutput[]
};

export type ImageModelOption = {
  id: string
  label: string
  note: string
  available: boolean
};

export type ImageModelsResponse = {
  default_model: string
  models: ImageModelOption[]
  key_configured: boolean
  key_source: 'image_text' | 'image'
  list_error?: string
};
