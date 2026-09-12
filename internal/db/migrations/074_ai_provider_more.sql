-- More AI providers in the catalog (#105 follow-up): all OpenAI-compatible,
-- so they work with the existing 'openai' wire style in ProviderClient.
-- Google via its OpenAI-compatibility endpoint.
-- Catalog data only -- NO secrets here. Secrets live on _ai_connection.
INSERT INTO _ai_provider (slug, display_name, base_url, api_style, default_list_models_path, provider_metadata)
VALUES
	('google', 'Google Gemini', 'https://generativelanguage.googleapis.com/v1beta/openai', 'openai', '/models',
	 '{"auth_header":"Authorization","auth_scheme":"Bearer"}'::jsonb),
	('groq', 'Groq', 'https://api.groq.com/openai/v1', 'openai', '/models',
	 '{"auth_header":"Authorization","auth_scheme":"Bearer"}'::jsonb),
	('mistral', 'Mistral', 'https://api.mistral.ai/v1', 'openai', '/models',
	 '{"auth_header":"Authorization","auth_scheme":"Bearer"}'::jsonb),
	('openrouter', 'OpenRouter', 'https://openrouter.ai/api/v1', 'openai', '/models',
	 '{"auth_header":"Authorization","auth_scheme":"Bearer"}'::jsonb)
ON CONFLICT (slug) DO NOTHING;
