import { Component } from '@angular/core';
import { RouterLink } from '@angular/router';
import { SiteHeader } from '../../shared/site-header';
import { SiteFooter } from '../../shared/site-footer';

@Component({
  selector: 'app-not-found',
  imports: [RouterLink, SiteHeader, SiteFooter],
  templateUrl: './not-found.html',
  styleUrl: './not-found.scss',
})
export class NotFoundPage {}
