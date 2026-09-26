// Copyright 2026 The Outline Authors
// Licensed under the Apache License, Version 2.0.

import {LitElement, css, html} from 'lit';
import {customElement, property, state} from 'lit/decorators.js';

import {pluginExec} from '../../app/plugin.cordova';

interface InstalledApplication {
  packageName: string;
  label: string;
  system: boolean;
}

interface Policy {
  mode: 'all' | 'bypass' | 'only';
  packages: string[];
  applications: InstalledApplication[];
}

@customElement('split-tunneling-view')
export class SplitTunnelingView extends LitElement {
  @property({type: String}) language = 'en';
  @state() private mode: Policy['mode'] = 'all';
  @state() private applications: InstalledApplication[] = [];
  @state() private selected = new Set<string>();
  @state() private search = '';
  @state() private showSystem = true;
  @state() private loading = true;
  @state() private saving = false;
  @state() private error = '';
  @state() private saved = false;

  static styles = css`
    :host {
      display: block;
      width: 100%;
      overflow: auto;
      background: var(--outline-background);
      color: var(--outline-text-color);
    }
    main {
      max-width: 720px;
      margin: auto;
      padding: 20px 18px 80px;
      box-sizing: border-box;
    }
    h2 {
      font-size: 19px;
      margin: 0 0 8px;
    }
    p {
      line-height: 1.45;
      opacity: 0.8;
      margin: 0 0 18px;
    }
    .mode,
    .app {
      display: flex;
      align-items: center;
      gap: 12px;
      padding: 13px 4px;
      border-bottom: 1px solid var(--outline-hairline);
      cursor: pointer;
    }
    .mode span,
    .app span {
      min-width: 0;
      flex: 1;
    }
    .app small {
      display: block;
      overflow-wrap: anywhere;
      opacity: 0.65;
      margin-top: 2px;
    }
    input[type='radio'],
    input[type='checkbox'] {
      width: 20px;
      height: 20px;
      accent-color: var(--outline-primary);
      flex: none;
    }
    input[type='search'] {
      box-sizing: border-box;
      width: 100%;
      min-height: 44px;
      margin: 16px 0 4px;
      padding: 8px 12px;
      border: 1px solid var(--outline-hairline);
      border-radius: 8px;
      background: var(--outline-background);
      color: var(--outline-text-color);
      font: inherit;
    }
    .actions {
      position: sticky;
      bottom: 0;
      padding: 14px 0;
      background: var(--outline-background);
    }
    button {
      border: 0;
      border-radius: 8px;
      min-height: 46px;
      padding: 0 20px;
      background: var(--outline-primary);
      color: white;
      font: inherit;
      cursor: pointer;
    }
    button:disabled {
      opacity: 0.5;
      cursor: default;
    }
    .notice {
      margin-top: 12px;
    }
    .error {
      color: #c62828;
    }
  `;

  connectedCallback() {
    super.connectedCallback();
    if (typeof cordova !== 'undefined' && cordova.platformId === 'android') {
      void this.load();
    }
  }

  private get ru() {
    return this.language.startsWith('ru');
  }
  private label(english: string, russian: string) {
    return this.ru ? russian : english;
  }

  private async load() {
    this.loading = true;
    try {
      const policy = await pluginExec<Policy>('getSplitTunneling');
      this.mode = policy.mode;
      this.selected = new Set(policy.packages);
      this.applications = policy.applications.sort((a, b) =>
        a.label.localeCompare(b.label, this.language)
      );
      this.error = '';
    } catch (error) {
      this.error = String(error);
    } finally {
      this.loading = false;
    }
  }

  private chooseMode(mode: Policy['mode']) {
    this.mode = mode;
    this.saved = false;
  }

  private togglePackage(packageName: string, checked: boolean) {
    const selected = new Set(this.selected);
    if (checked) selected.add(packageName);
    else selected.delete(packageName);
    this.selected = selected;
    this.saved = false;
  }

  private async save() {
    if (
      this.mode === 'only' &&
      !this.applications.some(app => this.selected.has(app.packageName))
    )
      return;
    this.saving = true;
    this.error = '';
    try {
      await pluginExec<void>('setSplitTunneling', this.mode, [
        ...this.selected,
      ]);
      this.saved = true;
    } catch (error) {
      this.error = String(error);
    } finally {
      this.saving = false;
    }
  }

  render() {
    const hasInstalledSelection = this.applications.some(app =>
      this.selected.has(app.packageName)
    );
    const query = this.search.trim().toLocaleLowerCase();
    const visible = this.applications.filter(
      app =>
        (this.showSystem ||
          !app.system ||
          this.selected.has(app.packageName)) &&
        (app.label.toLocaleLowerCase().includes(query) ||
          app.packageName.toLowerCase().includes(query))
    );
    return html`<main>
      <h2>${this.label('Split tunneling', 'Раздельное туннелирование')}</h2>
      <p>
        ${this.label(
          'Choose which apps use the VPN. Changes restart the active connection briefly.',
          'Выберите, какие приложения используют VPN. При сохранении активное подключение ненадолго перезапустится.'
        )}
      </p>
      ${(['all', 'bypass', 'only'] as const).map(
        mode =>
          html` <label class="mode"
            ><input
              type="radio"
              name="split-mode"
              .checked=${this.mode === mode}
              @change=${() => this.chooseMode(mode)}
            />
            <span
              >${mode === 'all'
                ? this.label('All apps through VPN', 'Все приложения через VPN')
                : mode === 'bypass'
                  ? this.label(
                      'Selected apps bypass VPN',
                      'Выбранные приложения мимо VPN'
                    )
                  : this.label(
                      'Only selected apps through VPN',
                      'Только выбранные приложения через VPN'
                    )}</span
            ></label
          >`
      )}
      ${this.mode !== 'all'
        ? html`
            <input
              type="search"
              .value=${this.search}
              @input=${(e: Event) =>
                (this.search = (e.target as HTMLInputElement).value)}
              placeholder=${this.label(
                'Search apps or package names',
                'Поиск приложений и пакетов'
              )}
              aria-label=${this.label('Search apps', 'Поиск приложений')}
            />
            <label class="mode"
              ><input
                type="checkbox"
                .checked=${this.showSystem}
                @change=${(e: Event) =>
                  (this.showSystem = (e.target as HTMLInputElement).checked)}
              />
              <span
                >${this.label(
                  'Show system apps',
                  'Показывать системные приложения'
                )}</span
              ></label
            >
            ${this.loading
              ? html`<p>
                  ${this.label('Loading apps…', 'Загрузка приложений…')}
                </p>`
              : visible.map(
                  app =>
                    html` <label class="app"
                      ><input
                        type="checkbox"
                        .checked=${this.selected.has(app.packageName)}
                        @change=${(e: Event) =>
                          this.togglePackage(
                            app.packageName,
                            (e.target as HTMLInputElement).checked
                          )}
                      />
                      <span
                        >${app.label}<small>${app.packageName}</small></span
                      ></label
                    >`
                )}
            ${!this.loading && !visible.length
              ? html`<p>
                  ${this.label('No apps found', 'Приложения не найдены')}
                </p>`
              : ''}
          `
        : ''}
      <div class="actions">
        <button
          ?disabled=${this.loading ||
          this.saving ||
          (this.mode === 'only' && !hasInstalledSelection)}
          @click=${this.save}
        >
          ${this.saving
            ? this.label('Saving…', 'Сохранение…')
            : this.label('Save', 'Сохранить')}
        </button>
        ${this.mode === 'only' && !hasInstalledSelection
          ? html`<p class="notice">
              ${this.label(
                'Select at least one installed app.',
                'Выберите хотя бы одно установленное приложение.'
              )}
            </p>`
          : ''}
        ${this.saved
          ? html`<p class="notice">
              ${this.label(
                'Saved. The active connection is restarting.',
                'Сохранено. Активное подключение перезапускается.'
              )}
            </p>`
          : ''}
        ${this.error ? html`<p class="notice error">${this.error}</p>` : ''}
      </div>
    </main>`;
  }
}
