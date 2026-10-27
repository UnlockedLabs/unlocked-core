{{/*
Helpers for the unlocked-core chart. Prefixed `unlocked-core.*` (not
`unlocked.*`) so these never collide with unlocked-platform's own
_helpers.tpl if this chart is later pulled in as a dependency there — Helm
merges named templates from a chart and all its dependencies into one global
namespace, and a duplicate name anywhere in the tree is a silent last-wins
collision.
*/}}

{{/* App hostname. Required. */}}
{{- define "unlocked-core.host" -}}
{{- required "host must be set" .Values.host -}}
{{- end }}

{{/*
Image reference for a component: repository:tag. tag defaults to the
chart's own AppVersion (set to the release tag by CI) - this chart's
versioning is lockstep with the image by design, so an unset tag should
mean "the image this chart release shipped with," not a render-time error.
Override only for a deliberate mismatch (e.g. testing an older image).
Usage: {{ include "unlocked-core.image" (list . "server") }}
*/}}
{{- define "unlocked-core.image" -}}
{{- $root := index . 0 -}}
{{- $name := index . 1 -}}
{{- $img := index $root.Values.images $name -}}
{{- $repo := required (printf "images.%s.repository must be set" $name) $img.repository -}}
{{- $tag := $img.tag | default $root.Chart.AppVersion -}}
{{- printf "%s:%s" $repo (toString $tag) -}}
{{- end }}

{{/*
imagePullPolicy line for a component: images.<name>.pullPolicy, else
images.pullPolicy. Emits a full "imagePullPolicy: X" line, or nothing.
Usage: {{- include "unlocked-core.pullPolicy" (list . "server") | nindent 8 }}
*/}}
{{- define "unlocked-core.pullPolicy" -}}
{{- $root := index . 0 -}}
{{- $name := index . 1 -}}
{{- $img := index $root.Values.images $name -}}
{{- $policy := $img.pullPolicy | default $root.Values.images.pullPolicy -}}
{{- if $policy }}imagePullPolicy: {{ $policy }}{{ end -}}
{{- end }}

{{/* Deployment strategy for a component (default Recreate). */}}
{{- define "unlocked-core.strategy" -}}
{{- $root := index . 0 -}}
{{- $name := index . 1 -}}
{{- index $root.Values.strategy $name | default "Recreate" -}}
{{- end }}

{{/*
initContainer that waits until NATS accepts connections. Shared by every
workload that needs NATS (server, scheduler) — platform's chart has the same
helper (unlocked.waitForNats) for the same reason: two copies of a retry
loop drift the moment one gets a fix (a timeout, a digest bump) the other
doesn't.
Usage: {{- include "unlocked-core.waitForNats" . | nindent 6 }}
*/}}
{{- define "unlocked-core.waitForNats" -}}
- name: wait-for-nats
  image: {{ .Values.waitForNats.image }}
  command: ["sh", "-c", "until nc -z -w 2 {{ .Values.nats.host }} {{ .Values.nats.port }}; do echo waiting for nats; sleep 2; done"]
{{- end }}

{{/*
DB connection env vars: APP_DSN (external RDS) or DB_HOST/PORT/USER/NAME/
PASSWORD (in-cluster), chosen by database.useAppDsn. Shared by every
workload that talks to Postgres (server, scheduler, and the migrate Job)
so the connection contract only has one place to change.
Usage: {{- include "unlocked-core.dbEnv" . | nindent 8 }}
*/}}
{{- define "unlocked-core.dbEnv" -}}
{{- if .Values.database.useAppDsn -}}
- name: APP_DSN
  valueFrom:
    secretKeyRef:
      name: {{ .Values.secretName }}
      key: APP_DSN
{{- else -}}
- name: DB_HOST
  value: {{ .Values.database.host | quote }}
- name: DB_PORT
  value: {{ .Values.database.port | quote }}
- name: DB_USER
  value: {{ .Values.database.user | quote }}
- name: DB_NAME
  value: {{ .Values.database.name | quote }}
- name: DB_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ .Values.secretName }}
      key: DB_PASSWORD
{{- end }}
{{- end }}

{{/*
Volume settings for one volume key, with required values checked.
Usage: {{ $v := include "unlocked-core.volume" (list . "logs") | fromYaml }}
*/}}
{{- define "unlocked-core.volume" -}}
{{- $root := index . 0 -}}
{{- $name := index . 1 -}}
{{- $v := index $root.Values.volumes $name -}}
pvName: {{ required (printf "volumes.%s.pvName must be set" $name) $v.pvName }}
pvcName: {{ required (printf "volumes.%s.pvcName must be set" $name) $v.pvcName }}
hostPath: {{ required (printf "volumes.%s.hostPath must be set" $name) $v.hostPath | quote }}
size: {{ required (printf "volumes.%s.size must be set" $name) $v.size }}
claimSize: {{ $v.claimSize | default $v.size }}
podVolume: {{ $v.podVolume | default $v.pvcName }}
accessMode: {{ $v.accessMode | default $root.Values.volumeDefaults.accessMode }}
storageClassName: {{ $v.storageClassName | default $root.Values.volumeDefaults.storageClassName }}
reclaimPolicy: {{ $v.reclaimPolicy | default $root.Values.volumeDefaults.reclaimPolicy }}
{{- end }}
