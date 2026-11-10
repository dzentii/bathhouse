import { Component, inject } from '@angular/core';
import { RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { CustomerAuth } from '../../core/customer-auth.service';
import { SiteHeader } from '../../shared/site-header';
import { SiteFooter } from '../../shared/site-footer';

@Component({
  selector: 'app-cabinet-layout',
  imports: [RouterLink, RouterLinkActive, RouterOutlet, SiteHeader, SiteFooter],
  templateUrl: './layout.html',
  styleUrl: './layout.scss',
})
export class CabinetLayout {
  readonly auth = inject(CustomerAuth);
}
